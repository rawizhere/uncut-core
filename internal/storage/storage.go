// Package storage gives every uncut database one sqlite Open: WAL, 0600,
// migrations keyed off PRAGMA user_version.
package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// Migration is one schema step; versions are contiguous, applied in order.
type Migration struct {
	Version    int
	Statements []string
}

// Options tunes the shared connection setup. Zero values fall back to the behaviour both stores already had.
type Options struct {
	// BusyTimeout is how long a write waits on a lock; default 5s.
	BusyTimeout time.Duration
	// DisableWAL turns off journal_mode(WAL); WAL is on by default.
	DisableWAL bool
	// ForeignKeys enables the foreign_keys pragma: every reference the schema declares is enforced at write time.
	ForeignKeys bool
}

// Open creates the database if needed, pins one connection, 0600, migrates.
func Open(path string, opts Options, migrations []Migration) (*sql.DB, error) {
	if err := validateMigrations(migrations); err != nil {
		return nil, err
	}

	busy := opts.BusyTimeout
	if busy <= 0 {
		busy = 5 * time.Second
	}

	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)", path, busy.Milliseconds())
	if !opts.DisableWAL {
		dsn += "&_pragma=journal_mode(WAL)"
	}
	if opts.ForeignKeys {
		dsn += "&_pragma=foreign_keys(1)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite db: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		_ = db.Close()
		return nil, fmt.Errorf("restrict db permissions: %w", err)
	}

	if err := migrate(db, migrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return db, nil
}

// migrate advances user_version to the newest step, one transaction per step.
func migrate(db *sql.DB, migrations []Migration) error {
	current, err := userVersion(db)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("migration v%d: %w", m.Version, err)
		}
	}
	return nil
}

func applyMigration(db *sql.DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range m.Statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("schema statement: %w", err)
		}
	}
	// PRAGMA user_version = N cannot take a bound parameter.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
		return fmt.Errorf("set user_version: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

func userVersion(db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return v, nil
}

func validateMigrations(migrations []Migration) error {
	last := 0
	for _, m := range migrations {
		if m.Version <= 0 {
			return fmt.Errorf("migration version %d: versions are 1-based", m.Version)
		}
		if m.Version != last+1 {
			return fmt.Errorf("migration versions must be contiguous: got v%d after v%d", m.Version, last)
		}
		if len(m.Statements) == 0 {
			return fmt.Errorf("migration v%d: no statements", m.Version)
		}
		last = m.Version
	}
	return nil
}
