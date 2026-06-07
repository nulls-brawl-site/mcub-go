// Package database provides a simple SQLite-backed key-value store for MCUB.
// It mirrors the aiosqlite KV store from the Python original.
package database

import (
	"database/sql"
	"fmt"
	"sync"

	// cgo SQLite driver
	_ "github.com/mattn/go-sqlite3"
)

const schema = `
CREATE TABLE IF NOT EXISTS kv (
    key   TEXT NOT NULL PRIMARY KEY,
    value TEXT NOT NULL
);
`

// Database is a thread-safe SQLite KV store.
type Database struct {
	db  *sql.DB
	mu  sync.RWMutex
	ver int
}

// Open opens (or creates) the SQLite database at path and initialises the schema.
func Open(path string, version int) (*Database, error) {
	db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	d := &Database{db: db, ver: version}
	if err := d.migrate(version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// migrate applies any version-based schema changes.
func (d *Database) migrate(targetVer int) error {
	// Read current stored version.
	var stored int
	row := d.db.QueryRow(`SELECT value FROM kv WHERE key='__db_version__'`)
	if err := row.Scan(&stored); err != nil {
		if err != sql.ErrNoRows {
			return fmt.Errorf("read db_version: %w", err)
		}
		// Not yet set – store current target.
		_, err = d.db.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES ('__db_version__', ?)`,
			fmt.Sprintf("%d", targetVer))
		return err
	}
	// Future migration steps can be added here based on stored < targetVer.
	_ = stored
	return nil
}

// Get retrieves the value for key. Returns ("", false, nil) if not found.
func (d *Database) Get(key string) (string, bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var val string
	err := d.db.QueryRow(`SELECT value FROM kv WHERE key=?`, key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get %q: %w", key, err)
	}
	return val, true, nil
}

// Set stores key=value. Creates or replaces an existing entry.
func (d *Database) Set(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)`, key, value)
	if err != nil {
		return fmt.Errorf("set %q: %w", key, err)
	}
	return nil
}

// Delete removes key from the store. Returns nil even if the key did not exist.
func (d *Database) Delete(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`DELETE FROM kv WHERE key=?`, key)
	if err != nil {
		return fmt.Errorf("delete %q: %w", key, err)
	}
	return nil
}

// Keys returns all keys whose prefix matches the given prefix string.
// Pass "" to return all keys.
func (d *Database) Keys(prefix string) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`SELECT key FROM kv WHERE key LIKE ? ORDER BY key`,
		prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("keys: %w", err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// Has returns true if key exists in the store.
func (d *Database) Has(key string) (bool, error) {
	_, found, err := d.Get(key)
	return found, err
}

// Version returns the db_version value recorded in the store.
func (d *Database) Version() int {
	return d.ver
}

// Close closes the underlying SQLite connection.
func (d *Database) Close() error {
	return d.db.Close()
}
