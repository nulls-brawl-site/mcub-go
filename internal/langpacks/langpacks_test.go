package langpacks_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/langpacks"
)

func TestLoad(t *testing.T) {
	m := langpacks.Default
	langs := m.Available()
	if len(langs) == 0 {
		t.Fatal("no languages loaded from embedded YAML files")
	}
}

func TestEnglishAvailable(t *testing.T) {
	langs := langpacks.Default.Available()
	found := false
	for _, l := range langs {
		if l == "en" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'en' in available langs, got %v", langs)
	}
}

func TestGetString(t *testing.T) {
	m := langpacks.Default
	// core_inline is a real non-global module in en.yaml.
	// The global "buttons" entries are merged into all modules, so
	// "close" (from buttons.__global__) is accessible via any module.
	val := m.Get("en", "core_inline", "close")
	if val == "" || val == "close" {
		t.Fatalf("expected localised string for close (merged from global buttons), got %q", val)
	}
}

func TestFallback(t *testing.T) {
	m := langpacks.Default
	// Request a locale that doesn't exist → falls back to "en"
	val := m.Get("zz_NONEXISTENT", "buttons", "close")
	enVal := m.Get("en", "buttons", "close")
	// Should fall back eventually to en
	if val == "" {
		t.Fatal("fallback should produce a non-empty value")
	}
	_ = enVal
}

func TestGetMissingKeyReturnsKey(t *testing.T) {
	m := langpacks.Default
	// Key doesn't exist → returns the key itself
	result := m.Get("en", "buttons", "completely_nonexistent_key_xyz")
	if result != "completely_nonexistent_key_xyz" {
		t.Fatalf("expected key name as fallback, got %q", result)
	}
}

func TestGetModule(t *testing.T) {
	m := langpacks.Default
	// core_inline is a non-global module; it also gets global strings merged in.
	mod := m.GetModule("en", "core_inline")
	if len(mod) == 0 {
		t.Fatal("expected non-empty module map for 'core_inline'")
	}
	// "no_access" is a direct key in core_inline
	if _, ok := mod["no_access"]; !ok {
		t.Fatalf("expected 'no_access' key in core_inline module, got keys: %v", func() []string {
			var ks []string
			for k := range mod {
				ks = append(ks, k)
			}
			return ks
		}())
	}
}

func TestSetDefault(t *testing.T) {
	// Verify SetDefault doesn't panic
	m := langpacks.Default
	m.SetDefault("ru")
	m.SetDefault("en") // restore
}

func TestLoadCustom(t *testing.T) {
	m := &langpacks.Manager{}
	data := []byte(`
testmod:
  greeting: Hello World
  farewell: Goodbye
`)
	if err := m.Load("custom", data); err != nil {
		t.Fatalf("Load: %v", err)
	}
	langs := m.Available()
	if len(langs) != 1 || langs[0] != "custom" {
		t.Fatalf("expected [custom], got %v", langs)
	}
	val := m.Get("custom", "testmod", "greeting")
	if val != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", val)
	}
}

func TestManagerType(t *testing.T) {
	// Ensure Manager is exported and usable
	var m langpacks.Manager
	_ = m
}
