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

func New(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", dbPath)

	db, err := sql.Open("sqlite", dsn)
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

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initSchema() error {
	query := `
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
	);`

	_, err := s.db.Exec(query)
	return err
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

func (s *Store) GetClients() ([]config.Client, error) {
	query := `SELECT uuid, name, password, sub_hash, protocols, created_at FROM clients ORDER BY created_at ASC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var clients []config.Client
	for rows.Next() {
		var c config.Client
		var protosJSON string
		var createdAt any

		if err := rows.Scan(&c.UUID, &c.Name, &c.Password, &c.SubHash, &protosJSON, &createdAt); err != nil {
			return nil, err
		}

		if protosJSON != "" {
			_ = json.Unmarshal([]byte(protosJSON), &c.Protocols)
		}
		if c.Protocols == nil {
			c.Protocols = []string{}
		}

		c.CreatedAt = parseTime(createdAt)
		clients = append(clients, c)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return clients, nil
}

func (s *Store) GetClientByUUID(uuid string) (*config.Client, error) {
	query := `SELECT uuid, name, password, sub_hash, protocols, created_at FROM clients WHERE uuid = ?`
	var c config.Client
	var protosJSON string
	var createdAt any

	err := s.db.QueryRow(query, uuid).Scan(&c.UUID, &c.Name, &c.Password, &c.SubHash, &protosJSON, &createdAt)
	if err != nil {
		return nil, err
	}

	if protosJSON != "" {
		_ = json.Unmarshal([]byte(protosJSON), &c.Protocols)
	}
	if c.Protocols == nil {
		c.Protocols = []string{}
	}
	c.CreatedAt = parseTime(createdAt)

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

	query := `INSERT INTO clients (uuid, name, password, sub_hash, protocols, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			name = excluded.name,
			password = excluded.password,
			sub_hash = excluded.sub_hash,
			protocols = excluded.protocols,
			created_at = excluded.created_at`

	_, err = s.db.Exec(query, client.UUID, client.Name, client.Password, client.SubHash, string(protosBytes), client.CreatedAt.Format(time.RFC3339))
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
