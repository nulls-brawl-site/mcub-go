package database_test

import (
	"database/sql"
	"fmt"
	"os"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/database"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp("", "mcub_test_*.db")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	f.Close()
	os.Remove(f.Name())
	t.Cleanup(func() { os.Remove(f.Name()) })
	return f.Name()
}

func TestOpenClose(t *testing.T) {
	path := tmpPath(t)
	db, err := database.Open(path, 2)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSetGet(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Set("hello", "world"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	val, found, err := db.Get("hello")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || val != "world" {
		t.Fatalf("expected world, got %q (found=%v)", val, found)
	}
}

func TestGetMissing(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, found, err := db.Get("nokey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Fatal("expected not found for missing key")
	}
}

func TestDelete(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete("k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, found, _ := db.Get("k")
	if found {
		t.Fatal("key should have been deleted")
	}
	// Delete non-existent key should not error
	if err := db.Delete("nonexistent"); err != nil {
		t.Fatalf("Delete non-existent: %v", err)
	}
}

func TestKeys(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, k := range []string{"aa", "ab", "bc"} {
		db.Set(k, "x")
	}

	keys, err := db.Keys("a")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys with prefix 'a', got %d: %v", len(keys), keys)
	}

	all, err := db.AllKeys()
	if err != nil {
		t.Fatal(err)
	}
	// all keys include __db_version__ plus our 3
	found := 0
	for _, k := range all {
		if k == "aa" || k == "ab" || k == "bc" {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("expected 3 user keys in AllKeys, got %d", found)
	}
}

func TestModuleScoped(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.ModuleSet("mymod", "key1", "val1"); err != nil {
		t.Fatalf("ModuleSet: %v", err)
	}
	val, found, err := db.ModuleGet("mymod", "key1")
	if err != nil {
		t.Fatalf("ModuleGet: %v", err)
	}
	if !found || val != "val1" {
		t.Fatalf("expected val1, got %q (found=%v)", val, found)
	}

	// Different module should not see it
	_, found2, _ := db.ModuleGet("othermod", "key1")
	if found2 {
		t.Fatal("other module should not see key")
	}

	// ModuleKeys
	mk, err := db.ModuleKeys("mymod")
	if err != nil {
		t.Fatal(err)
	}
	if len(mk) != 1 || mk[0] != "key1" {
		t.Fatalf("expected [key1], got %v", mk)
	}
}

func TestSetJSON(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type Thing struct {
		Name string `json:"name"`
		Val  int    `json:"val"`
	}

	orig := Thing{Name: "foo", Val: 42}
	if err := db.SetJSON("obj", orig); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}

	var got Thing
	if err := db.GetJSON("obj", &got); err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if got != orig {
		t.Fatalf("expected %+v, got %+v", orig, got)
	}
}

func TestGetJSONMissing(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var v int
	err = db.GetJSON("nokey", &v)
	if err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestTransaction(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = db.Transaction(func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)`, "tx_key", "tx_val")
		return err
	})
	if err != nil {
		t.Fatalf("Transaction: %v", err)
	}

	val, found, _ := db.Get("tx_key")
	if !found || val != "tx_val" {
		t.Fatalf("expected tx_val, got %q found=%v", val, found)
	}
}

func TestTransactionRollback(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Set("before", "yes")
	err = db.Transaction(func(tx *sql.Tx) error {
		tx.Exec(`INSERT OR REPLACE INTO kv (key, value) VALUES (?, ?)`, "rollback_key", "v")
		return fmt.Errorf("intentional error")
	})
	if err == nil {
		t.Fatal("expected transaction error")
	}
	_, found, _ := db.Get("rollback_key")
	if found {
		t.Fatal("rollback_key should not exist after rollback")
	}
}

func TestDeletePrefix(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, k := range []string{"pfx:a", "pfx:b", "other:c"} {
		db.Set(k, "x")
	}

	n, err := db.DeletePrefix("pfx:")
	if err != nil {
		t.Fatalf("DeletePrefix: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 deleted, got %d", n)
	}

	_, found, _ := db.Get("other:c")
	if !found {
		t.Fatal("other:c should still exist")
	}
}

func TestGetOrDefault(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	val, err := db.GetOrDefault("missing", "default")
	if err != nil {
		t.Fatal(err)
	}
	if val != "default" {
		t.Fatalf("expected default, got %q", val)
	}

	db.Set("present", "here")
	val, err = db.GetOrDefault("present", "default")
	if err != nil {
		t.Fatal(err)
	}
	if val != "here" {
		t.Fatalf("expected here, got %q", val)
	}
}

func TestVersion(t *testing.T) {
	db, err := database.Open(tmpPath(t), 5)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if v := db.Version(); v != 5 {
		t.Fatalf("expected version 5, got %d", v)
	}
}

func TestCount(t *testing.T) {
	db, err := database.Open(tmpPath(t), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	n, err := db.Count()
	if err != nil {
		t.Fatal(err)
	}
	// starts with 1 (__db_version__)
	if n < 1 {
		t.Fatalf("expected at least 1 row (db_version), got %d", n)
	}

	db.Set("k1", "v1")
	db.Set("k2", "v2")
	n2, _ := db.Count()
	if n2 != n+2 {
		t.Fatalf("expected %d rows, got %d", n+2, n2)
	}
}
