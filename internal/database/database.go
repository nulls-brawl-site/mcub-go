// Package database provides a simple SQLite-backed key-value store for MCUB.
// It mirrors the aiosqlite KV store from the Python original (database.py).
package database

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
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
	db   *sql.DB
	mu   sync.RWMutex
	ver  int
	path string
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
	d := &Database{db: db, ver: version, path: path}
	if err := d.migrate(version); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return d, nil
}

// migrate applies any version-based schema changes.
func (d *Database) migrate(targetVer int) error {
	var stored int
	row := d.db.QueryRow(`SELECT value FROM kv WHERE key='__db_version__'`)
	if err := row.Scan(&stored); err != nil {
		if err != sql.ErrNoRows {
			return fmt.Errorf("read db_version: %w", err)
		}
		_, err = d.db.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES ('__db_version__', ?)`,
			fmt.Sprintf("%d", targetVer))
		return err
	}
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

// Keys returns all keys whose value starts with the given prefix.
// Pass "" to return all keys (same as AllKeys).
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

// GetOrDefault returns the value for key, or defaultVal if the key is absent.
func (d *Database) GetOrDefault(key, defaultVal string) (string, error) {
	val, found, err := d.Get(key)
	if err != nil {
		return defaultVal, err
	}
	if !found {
		return defaultVal, nil
	}
	return val, nil
}

// SetJSON marshals value to JSON and stores the result under key.
func (d *Database) SetJSON(key string, value interface{}) error {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal json for key %q: %w", key, err)
	}
	return d.Set(key, string(b))
}

// GetJSON retrieves the value for key and unmarshals it into target.
// Returns sql.ErrNoRows if the key does not exist.
func (d *Database) GetJSON(key string, target interface{}) error {
	val, found, err := d.Get(key)
	if err != nil {
		return err
	}
	if !found {
		return sql.ErrNoRows
	}
	if err := json.Unmarshal([]byte(val), target); err != nil {
		return fmt.Errorf("unmarshal json for key %q: %w", key, err)
	}
	return nil
}

// DeletePrefix removes all keys whose key starts with prefix.
// It returns the number of rows deleted.
func (d *Database) DeletePrefix(prefix string) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	res, err := d.db.Exec(`DELETE FROM kv WHERE key LIKE ?`, prefix+"%")
	if err != nil {
		return 0, fmt.Errorf("delete prefix %q: %w", prefix, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// AllKeys returns every key in the database.
func (d *Database) AllKeys() ([]string, error) {
	return d.Keys("")
}

// Count returns the total number of entries in the store.
func (d *Database) Count() (int, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	var n int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM kv`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// Vacuum runs SQLite's VACUUM command to reclaim unused space.
func (d *Database) Vacuum() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, err := d.db.Exec(`VACUUM`)
	if err != nil {
		return fmt.Errorf("vacuum: %w", err)
	}
	return nil
}

// Backup copies the database file to destPath using a simple file copy.
// The store is read-locked during the copy so no writes occur concurrently.
func (d *Database) Backup(destPath string) error {
	if d.path == "" {
		return fmt.Errorf("backup: database path not known (in-memory database?)")
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	src, err := os.Open(d.path)
	if err != nil {
		return fmt.Errorf("backup open src %q: %w", d.path, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("backup create dst %q: %w", destPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("backup copy: %w", err)
	}
	return nil
}

// Transaction executes fn inside a database transaction. If fn returns an
// error the transaction is rolled back; otherwise it is committed.
func (d *Database) Transaction(fn func(tx *sql.Tx) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	tx, err := d.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// moduleKey returns the composite key used for module-scoped entries.
func moduleKey(module, key string) string {
	return module + ":" + key
}

// ModuleGet retrieves a value scoped to module with the given key.
// Returns ("", false, nil) when not found.
func (d *Database) ModuleGet(module, key string) (string, bool, error) {
	return d.Get(moduleKey(module, key))
}

// ModuleSet stores a value scoped to module under key.
func (d *Database) ModuleSet(module, key, value string) error {
	return d.Set(moduleKey(module, key), value)
}

// ModuleDelete removes the value scoped to module under key.
func (d *Database) ModuleDelete(module, key string) error {
	return d.Delete(moduleKey(module, key))
}

// ModuleKeys returns all keys stored for module with the module prefix stripped.
func (d *Database) ModuleKeys(module string) ([]string, error) {
	prefix := module + ":"
	raw, err := d.Keys(prefix)
	if err != nil {
		return nil, err
	}
	// Strip the "module:" prefix from each key.
	out := make([]string, 0, len(raw))
	for _, k := range raw {
		out = append(out, strings.TrimPrefix(k, prefix))
	}
	return out, nil
}

// Version returns the db_version value recorded in the store.
func (d *Database) Version() int {
	return d.ver
}

// Close closes the underlying SQLite connection.
func (d *Database) Close() error {
	return d.db.Close()
}
