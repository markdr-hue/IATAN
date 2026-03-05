/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package db

import (
	"database/sql"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

//go:embed site_migrations/*.sql
var siteMigrationFS embed.FS

// SiteDB wraps a per-site SQLite database with a dedicated writer pool.
// The read pool (embedded *sql.DB) allows concurrent reads via WAL mode.
// The writer pool (single connection) serializes all writes at the connection level,
// eliminating SQLITE_BUSY errors without needing a Go-level mutex.
type SiteDB struct {
	*sql.DB        // read pool (concurrent reads)
	writer  *sql.DB // write pool (single connection, serializes writes)
	SiteID  int
}

// Writer returns the single-connection write pool for direct use.
func (s *SiteDB) Writer() *sql.DB {
	return s.writer
}

// ExecWrite executes a write query through the serialized writer pool.
func (s *SiteDB) ExecWrite(query string, args ...interface{}) (sql.Result, error) {
	return s.writer.Exec(query, args...)
}

// BeginWriteTx starts a transaction on the writer pool.
func (s *SiteDB) BeginWriteTx() (*sql.Tx, error) {
	return s.writer.Begin()
}

// QueryWriter executes a query on the writer pool (for writes that return rows).
func (s *SiteDB) QueryWriter(query string, args ...interface{}) (*sql.Rows, error) {
	return s.writer.Query(query, args...)
}

// QueryRowWriter executes a query on the writer pool that returns a single row.
func (s *SiteDB) QueryRowWriter(query string, args ...interface{}) *sql.Row {
	return s.writer.QueryRow(query, args...)
}

// SiteDBManager handles the lifecycle of per-site databases.
type SiteDBManager struct {
	mu      sync.RWMutex
	dataDir string
	dbs     map[int]*SiteDB
}

// NewSiteDBManager creates a manager for per-site databases.
func NewSiteDBManager(dataDir string) *SiteDBManager {
	return &SiteDBManager{
		dataDir: dataDir,
		dbs:     make(map[int]*SiteDB),
	}
}

// dbPath returns the filesystem path for a site's database.
func (m *SiteDBManager) dbPath(siteID int) string {
	return filepath.Join(m.dataDir, "sites", fmt.Sprintf("%d", siteID), "site.db")
}

// Open opens (or returns a cached) site database. Runs migrations on first open.
func (m *SiteDBManager) Open(siteID int) (*SiteDB, error) {
	m.mu.RLock()
	if sdb, ok := m.dbs[siteID]; ok {
		m.mu.RUnlock()
		return sdb, nil
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if sdb, ok := m.dbs[siteID]; ok {
		return sdb, nil
	}

	sdb, err := m.openSiteDB(siteID)
	if err != nil {
		return nil, err
	}

	m.dbs[siteID] = sdb
	return sdb, nil
}

// Create creates a new site database and runs migrations.
func (m *SiteDBManager) Create(siteID int) (*SiteDB, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sdb, ok := m.dbs[siteID]; ok {
		return sdb, nil
	}

	// Ensure the site directory exists.
	dbPath := m.dbPath(siteID)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("creating site db directory: %w", err)
	}

	sdb, err := m.openSiteDB(siteID)
	if err != nil {
		return nil, err
	}

	m.dbs[siteID] = sdb
	slog.Info("site database created", "site_id", siteID, "path", dbPath)
	return sdb, nil
}

// Close closes a site's database and removes it from the cache.
func (m *SiteDBManager) Close(siteID int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sdb, ok := m.dbs[siteID]; ok {
		sdb.writer.Close()
		sdb.DB.Close()
		delete(m.dbs, siteID)
		slog.Info("site database closed", "site_id", siteID)
	}
}

// Delete closes a site's database and removes the DB file from disk.
func (m *SiteDBManager) Delete(siteID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sdb, ok := m.dbs[siteID]; ok {
		sdb.writer.Close()
		sdb.DB.Close()
		delete(m.dbs, siteID)
	}

	// Remove the entire site directory (includes site.db, assets, files).
	siteDir := filepath.Join(m.dataDir, "sites", fmt.Sprintf("%d", siteID))
	if err := os.RemoveAll(siteDir); err != nil {
		return fmt.Errorf("removing site directory: %w", err)
	}

	slog.Info("site database deleted", "site_id", siteID)
	return nil
}

// Get returns a cached site database or nil if not open.
func (m *SiteDBManager) Get(siteID int) *SiteDB {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.dbs[siteID]
}

// CloseAll closes all open site databases. Called on shutdown.
func (m *SiteDBManager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, sdb := range m.dbs {
		sdb.writer.Close()
		sdb.DB.Close()
		slog.Info("site database closed", "site_id", id)
	}
	m.dbs = make(map[int]*SiteDB)
}

// OpenSiteIDs returns a list of all currently open site IDs.
func (m *SiteDBManager) OpenSiteIDs() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]int, 0, len(m.dbs))
	for id := range m.dbs {
		ids = append(ids, id)
	}
	return ids
}

// openSiteDB opens the SQLite file and runs site migrations. Must be called with m.mu held.
func (m *SiteDBManager) openSiteDB(siteID int) (*SiteDB, error) {
	dbPath := m.dbPath(siteID)

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("creating site db directory: %w", err)
	}

	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=1", dbPath)

	// Read pool — concurrent reads via WAL mode.
	readDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening site database %d (read): %w", siteID, err)
	}
	if err := readDB.Ping(); err != nil {
		readDB.Close()
		return nil, fmt.Errorf("pinging site database %d: %w", siteID, err)
	}
	readDB.SetMaxOpenConns(4)

	// Write pool — single connection serializes all writes at the DB level.
	// No Go-level mutex needed; Go's sql.DB queues callers when MaxOpenConns=1.
	writeDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		readDB.Close()
		return nil, fmt.Errorf("opening site database %d (write): %w", siteID, err)
	}
	if err := writeDB.Ping(); err != nil {
		readDB.Close()
		writeDB.Close()
		return nil, fmt.Errorf("pinging site database %d (write): %w", siteID, err)
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)

	sdb := &SiteDB{DB: readDB, writer: writeDB, SiteID: siteID}

	if err := runSiteMigrations(sdb); err != nil {
		writeDB.Close()
		readDB.Close()
		return nil, fmt.Errorf("running site migrations for site %d: %w", siteID, err)
	}

	slog.Info("site database initialized", "site_id", siteID, "path", dbPath)
	return sdb, nil
}

// runSiteMigrations applies pending site-level migrations.
func runSiteMigrations(sdb *SiteDB) error {
	_, err := sdb.Exec(`CREATE TABLE IF NOT EXISTS migrations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("creating site migrations table: %w", err)
	}

	entries, err := siteMigrationFS.ReadDir("site_migrations")
	if err != nil {
		return fmt.Errorf("reading site_migrations dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		name := entry.Name()

		var count int
		if err := sdb.QueryRow("SELECT COUNT(*) FROM migrations WHERE name = ?", name).Scan(&count); err != nil {
			return fmt.Errorf("checking site migration %s: %w", name, err)
		}
		if count > 0 {
			continue
		}

		content, err := siteMigrationFS.ReadFile("site_migrations/" + name)
		if err != nil {
			return fmt.Errorf("reading site migration %s: %w", name, err)
		}

		statements := strings.Split(string(content), ";")
		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			for strings.HasPrefix(stmt, "--") {
				if idx := strings.Index(stmt, "\n"); idx >= 0 {
					stmt = strings.TrimSpace(stmt[idx+1:])
				} else {
					stmt = ""
					break
				}
			}
			if stmt == "" {
				continue
			}
			if _, err := sdb.Exec(stmt); err != nil {
				return fmt.Errorf("executing site migration %s: %w\nStatement: %s", name, err, stmt)
			}
		}

		if _, err := sdb.Exec("INSERT INTO migrations (name) VALUES (?)", name); err != nil {
			return fmt.Errorf("recording site migration %s: %w", name, err)
		}

		slog.Info("applied site migration", "site_id", sdb.SiteID, "name", name)
	}

	return nil
}
