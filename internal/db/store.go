package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/rawizhere/uncut-core/internal/config"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

// migrations are schema steps keyed off PRAGMA user_version: entry i is version i+1, so versions stay contiguous.
// v1 reproduces the original CREATE TABLE block; schema edits append versions instead of editing old ones.
var migrations = []string{`
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT
);

CREATE TABLE IF NOT EXISTS clients (
	uuid TEXT PRIMARY KEY,
	name TEXT,
	password TEXT,
	sub_hash TEXT,
	protocols TEXT,
	created_at DATETIME
);`,
	// v2 renamed the country token to a free-form tag; the old key dies.
	`
INSERT INTO settings (key, value)
SELECT 'tag', value FROM settings WHERE key = 'country'
  AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'tag');

DELETE FROM settings WHERE key = 'country';
`,
	// v3 persists the per-client allowlist marker; before it every reload reverted to inherit.
	`ALTER TABLE clients ADD COLUMN protocols_explicit INTEGER NOT NULL DEFAULT 0`,
	// v4 backfills the subscription token of pre-token rows, which the render path used to write on the fly.
	`UPDATE clients SET sub_hash = lower(hex(randomblob(16))) WHERE sub_hash IS NULL OR sub_hash = ''`,
	// v5 stores the AmneziaWG peer material (curve25519 pair, preshared key, tunnel address) as one JSON blob.
	`ALTER TABLE clients ADD COLUMN awg TEXT NOT NULL DEFAULT ''`,
}

// New opens the node database: WAL, 5s busy timeout, one pinned connection, 0600, migrated.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite db: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = db.Close()
		return nil, fmt.Errorf("restrict db permissions: %w", err)
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate %s: %w", dbPath, err)
	}
	return &Store{db: db}, nil
}

// migrate applies every step past user_version, one transaction per step.
func migrate(db *sql.DB) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for v := current + 1; v <= len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("migration v%d: begin: %w", v, err)
		}
		_, err = tx.Exec(migrations[v-1])
		if err == nil {
			// PRAGMA user_version = N cannot take a bound parameter.
			_, err = tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v))
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration v%d: %w", v, err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// Backup writes a consistent snapshot to path, WAL included; a plain file copy would miss the pages still in the -wal.
func (s *Store) Backup(path string) error {
	_, err := s.db.Exec("VACUUM INTO ?", path)
	return err
}

// Restore replaces settings and clients with a backup's rows in one transaction; swapping the file under open connections would let the stale -wal win.
func (s *Store) Restore(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	// A scratch copy: opening it migrates an older backup to the current schema without touching the original.
	tmp := path + ".restore"
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(tmp + suffix)
		}
	}()
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("stage backup: %w", err)
	}
	staged, err := New(tmp)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	_ = staged.Close()

	ctx := context.Background()
	// ATTACH is per connection: pin one for the attach, the transaction and the detach.
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "ATTACH DATABASE ? AS b", tmp); err != nil {
		return fmt.Errorf("attach backup: %w", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, "DETACH DATABASE b") }()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM main.settings`,
		`INSERT INTO main.settings (key, value) SELECT key, value FROM b.settings`,
		`DELETE FROM main.clients`,
		`INSERT INTO main.clients (` + clientColumns + `) SELECT ` + clientColumns + ` FROM b.clients ORDER BY rowid`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("restore: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) GetSetting(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		return "", err
	}
	return val, nil
}

const upsertSetting = `INSERT INTO settings (key, value) VALUES (?, ?)
	ON CONFLICT(key) DO UPDATE SET value = excluded.value`

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(upsertSetting, key, value)
	return err
}

// SetSettings writes several keys in one transaction: a multi-key change lands whole or not at all.
func (s *Store) SetSettings(kv map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for k, v := range kv {
		if _, err := tx.Exec(upsertSetting, k, v); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set %s: %w", k, err)
		}
	}
	return tx.Commit()
}

const clientColumns = `uuid, name, password, sub_hash, protocols, protocols_explicit, awg, created_at`

func scanClient(row interface{ Scan(...any) error }) (config.Client, error) {
	var c config.Client
	var protosJSON, awgJSON string
	var createdAt any
	if err := row.Scan(&c.UUID, &c.Name, &c.Password, &c.SubHash, &protosJSON, &c.ProtocolsExplicit, &awgJSON, &createdAt); err != nil {
		return c, err
	}
	if protosJSON != "" {
		_ = json.Unmarshal([]byte(protosJSON), &c.Protocols)
	}
	if awgJSON != "" {
		_ = json.Unmarshal([]byte(awgJSON), &c.AWG)
	}
	if c.Protocols == nil {
		c.Protocols = []string{}
	}
	c.CreatedAt = parseTime(createdAt)
	return c, nil
}

func (s *Store) GetClients() ([]config.Client, error) {
	rows, err := s.db.Query(`SELECT ` + clientColumns + ` FROM clients ORDER BY rowid ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var clients []config.Client
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			return nil, err
		}
		clients = append(clients, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return clients, nil
}

func (s *Store) GetClientByUUID(uuid string) (*config.Client, error) {
	c, err := scanClient(s.db.QueryRow(`SELECT `+clientColumns+` FROM clients WHERE uuid = ?`, uuid))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) AddClient(client config.Client) error {
	if client.CreatedAt.IsZero() {
		client.CreatedAt = time.Now().UTC()
	}
	if client.Protocols == nil {
		client.Protocols = []string{}
	}

	protosBytes, err := json.Marshal(client.Protocols)
	if err != nil {
		return fmt.Errorf("marshal client protocols: %w", err)
	}
	awgBytes, err := json.Marshal(client.AWG)
	if err != nil {
		return fmt.Errorf("marshal client awg: %w", err)
	}

	query := `INSERT INTO clients (` + clientColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			name = excluded.name,
			password = excluded.password,
			sub_hash = excluded.sub_hash,
			protocols = excluded.protocols,
			protocols_explicit = excluded.protocols_explicit,
			awg = excluded.awg,
			created_at = excluded.created_at`

	_, err = s.db.Exec(query, client.UUID, client.Name, client.Password, client.SubHash, string(protosBytes), client.ProtocolsExplicit, string(awgBytes), client.CreatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) DeleteClient(uuid string) error {
	_, err := s.db.Exec("DELETE FROM clients WHERE uuid = ?", uuid)
	return err
}

func parseTime(val any) time.Time {
	switch v := val.(type) {
	case time.Time:
		return v
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return t
		}
		if t, err := time.Parse("2006-01-02 15:04:05", v); err == nil {
			return t
		}
	}
	return time.Time{}
}
