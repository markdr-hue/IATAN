/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
	mu sync.Mutex // serializes writes for SQLite
}

func Open(path string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1", path)
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Verify connection
	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	db := &DB{DB: sqlDB}

	// Run migrations
	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	slog.Info("database initialized", "path", path)
	return db, nil
}

// ExecWrite serializes write operations.
func (db *DB) ExecWrite(query string, args ...interface{}) (sql.Result, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.Exec(query, args...)
}

// BeginWriteTx starts a serialized write transaction. The caller must commit
// or rollback the returned transaction.
func (db *DB) BeginWriteTx() (*sql.Tx, error) {
	db.mu.Lock()
	tx, err := db.Begin()
	if err != nil {
		db.mu.Unlock()
		return nil, err
	}
	return tx, nil
}

// EndWriteTx must be called after BeginWriteTx to release the write lock.
// Typical usage:
//
//	tx, err := db.BeginWriteTx()
//	defer db.EndWriteTx()
//	// ... use tx ...
//	tx.Commit()
func (db *DB) EndWriteTx() {
	db.mu.Unlock()
}

// GetSetting retrieves a setting value by key
func (db *DB) GetSetting(key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	return value, err
}

// SetSetting sets a setting value
func (db *DB) SetSetting(key, value string) error {
	_, err := db.ExecWrite(
		`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP`,
		key, value,
	)
	return err
}
