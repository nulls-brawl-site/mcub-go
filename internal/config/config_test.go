package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestLoadOrCreate
// ─────────────────────────────────────────────────────────────────────────────

func TestLoadOrCreate_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg, created, err := config.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for a new file")
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	// Default values
	if cfg.CommandPrefix != "." {
		t.Errorf("default CommandPrefix: got %q, want %q", cfg.CommandPrefix, ".")
	}
	if cfg.Language != "en" {
		t.Errorf("default Language: got %q, want %q", cfg.Language, "en")
	}
	if cfg.Aliases == nil {
		t.Error("default Aliases should be non-nil map")
	}
	// File should now exist on disk
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config file not created on disk: %v", err)
	}
}

func TestLoadOrCreate_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Write a minimal valid config.
	raw := `{"api_id":12345,"api_hash":"abc","phone":"+1","command_prefix":"!"}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, created, err := config.LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate failed: %v", err)
	}
	if created {
		t.Fatal("expected created=false for an existing file")
	}
	if cfg.APIID != 12345 {
		t.Errorf("APIID: got %d, want 12345", cfg.APIID)
	}
	if cfg.CommandPrefix != "!" {
		t.Errorf("CommandPrefix: got %q, want %q", cfg.CommandPrefix, "!")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestSaveLoad
// ─────────────────────────────────────────────────────────────────────────────

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	original := &config.Config{
		APIID:         99999,
		APIHash:       "testhash",
		Phone:         "+9999999999",
		CommandPrefix: ";",
		Language:      "ru",
		Aliases:       map[string]string{"hi": "hello"},
	}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.APIID != original.APIID {
		t.Errorf("APIID mismatch: got %d, want %d", loaded.APIID, original.APIID)
	}
	if loaded.APIHash != original.APIHash {
		t.Errorf("APIHash mismatch")
	}
	if loaded.CommandPrefix != original.CommandPrefix {
		t.Errorf("CommandPrefix mismatch: got %q, want %q", loaded.CommandPrefix, original.CommandPrefix)
	}
	if loaded.Language != original.Language {
		t.Errorf("Language mismatch: got %q, want %q", loaded.Language, original.Language)
	}
	if len(loaded.Aliases) != 1 || loaded.Aliases["hi"] != "hello" {
		t.Errorf("Aliases mismatch: got %v", loaded.Aliases)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestGetSetModuleConfig
// ─────────────────────────────────────────────────────────────────────────────

func TestGetSetModuleConfig(t *testing.T) {
	cfg := &config.Config{
		ModuleConfigs: map[string]json.RawMessage{},
	}

	// Initially empty.
	got, err := cfg.GetModuleConfig("mymod", nil)
	if err != nil {
		t.Fatalf("GetModuleConfig (empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}

	// Set data.
	data := map[string]interface{}{"key": "value", "num": float64(42)}
	if err := cfg.SetModuleConfig("mymod", data); err != nil {
		t.Fatalf("SetModuleConfig: %v", err)
	}

	// Read it back.
	got, err = cfg.GetModuleConfig("mymod", nil)
	if err != nil {
		t.Fatalf("GetModuleConfig (after set): %v", err)
	}
	if got["key"] != "value" {
		t.Errorf("key mismatch: %v", got["key"])
	}
	if got["num"] != float64(42) {
		t.Errorf("num mismatch: %v", got["num"])
	}
}

func TestGetKey(t *testing.T) {
	cfg := &config.Config{
		ModuleConfigs: map[string]json.RawMessage{},
	}
	if err := cfg.SetModuleConfig("mod", map[string]interface{}{"x": "y"}); err != nil {
		t.Fatal(err)
	}

	v, err := cfg.GetKey("mod", "x", nil)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if v != "y" {
		t.Errorf("GetKey: got %v, want %q", v, "y")
	}

	// Missing key returns default.
	v, err = cfg.GetKey("mod", "missing", "default_val")
	if err != nil {
		t.Fatalf("GetKey missing: %v", err)
	}
	if v != "default_val" {
		t.Errorf("GetKey missing: got %v, want %q", v, "default_val")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestMerge
// ─────────────────────────────────────────────────────────────────────────────

func TestMerge(t *testing.T) {
	cfg := &config.Config{
		CommandPrefix: ".",
		Language:      "en",
	}

	cfg.Merge(map[string]interface{}{
		"command_prefix": "!",
		"language":       "ru",
		"power_save_mode": true,
	})

	if cfg.CommandPrefix != "!" {
		t.Errorf("CommandPrefix after merge: got %q, want %q", cfg.CommandPrefix, "!")
	}
	if cfg.Language != "ru" {
		t.Errorf("Language after merge: got %q, want %q", cfg.Language, "ru")
	}
	if !cfg.PowerSaveMode {
		t.Error("PowerSaveMode should be true after merge")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestValidate
// ─────────────────────────────────────────────────────────────────────────────

func TestValidate(t *testing.T) {
	// Valid config.
	good := &config.Config{
		APIID:         12345,
		APIHash:       "hash",
		Phone:         "+1234",
		CommandPrefix: ".",
	}
	if err := good.Validate(); err != nil {
		t.Errorf("Validate on good config: %v", err)
	}

	// Missing APIID.
	bad := &config.Config{
		APIHash:       "hash",
		Phone:         "+1",
		CommandPrefix: ".",
	}
	if err := bad.Validate(); err == nil {
		t.Error("expected error for missing api_id")
	}

	// Missing APIHash.
	bad2 := &config.Config{
		APIID:         1,
		Phone:         "+1",
		CommandPrefix: ".",
	}
	if err := bad2.Validate(); err == nil {
		t.Error("expected error for missing api_hash")
	}

	// Empty prefix.
	bad3 := &config.Config{
		APIID:   1,
		APIHash: "h",
		Phone:   "+1",
	}
	if err := bad3.Validate(); err == nil {
		t.Error("expected error for empty command_prefix")
	}
}
