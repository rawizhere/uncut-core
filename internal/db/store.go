package db

import (
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

func (s *Store) GetSetting(key string) (string, error) {
	var val string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&val)
	if err != nil {
		return "", err
	}
	return val, nil
}

func (s *Store) SetSetting(key, value string) error {
	query := `INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`
	_, err := s.db.Exec(query, key, value)
	return err
}

const clientColumns = `uuid, name, password, sub_hash, protocols, protocols_explicit, created_at`

func scanClient(row interface{ Scan(...any) error }) (config.Client, error) {
	var c config.Client
	var protosJSON string
	var createdAt any
	if err := row.Scan(&c.UUID, &c.Name, &c.Password, &c.SubHash, &protosJSON, &c.ProtocolsExplicit, &createdAt); err != nil {
		return c, err
	}
	if protosJSON != "" {
		_ = json.Unmarshal([]byte(protosJSON), &c.Protocols)
	}
	if c.Protocols == nil {
		c.Protocols = []string{}
	}
	c.CreatedAt = parseTime(createdAt)
	return c, nil
}

func (s *Store) GetClients() ([]config.Client, error) {
	rows, err := s.db.Query(`SELECT ` + clientColumns + ` FROM clients ORDER BY created_at ASC`)
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
		client.CreatedAt = config.GetMSKTime()
	}
	if client.Protocols == nil {
		client.Protocols = []string{}
	}

	protosBytes, err := json.Marshal(client.Protocols)
	if err != nil {
		return fmt.Errorf("marshal client protocols: %w", err)
	}

	query := `INSERT INTO clients (` + clientColumns + `)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			name = excluded.name,
			password = excluded.password,
			sub_hash = excluded.sub_hash,
			protocols = excluded.protocols,
			protocols_explicit = excluded.protocols_explicit,
			created_at = excluded.created_at`

	_, err = s.db.Exec(query, client.UUID, client.Name, client.Password, client.SubHash, string(protosBytes), client.ProtocolsExplicit, client.CreatedAt.Format(time.RFC3339))
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
	return config.GetMSKTime()
}
