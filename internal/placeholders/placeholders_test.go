package placeholders_test

import (
	"strings"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/placeholders"
)

// ---- NewRegistry / Register -------------------------------------------------

func TestNewRegistryNotNil(t *testing.T) {
	r := placeholders.NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
}

func TestRegisterAndResolve(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("scope", "greet", "greeting", func(data map[string]interface{}) (string, error) {
		return "hello", nil
	})
	result, err := r.Resolve("scope", "prefix {greet} suffix", nil, nil, false)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if result != "prefix hello suffix" {
		t.Errorf("got %q", result)
	}
}

func TestResolveNoPlaceholders(t *testing.T) {
	r := placeholders.NewRegistry()
	result, err := r.Resolve("scope", "plain text", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result != "plain text" {
		t.Errorf("got %q", result)
	}
}

func TestResolveUnknownPlaceholderNonStrict(t *testing.T) {
	r := placeholders.NewRegistry()
	// Unknown token in non-strict mode should leave token as-is.
	result, err := r.Resolve("scope", "hello {unknown}", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "{unknown}") {
		t.Errorf("expected unresolved token, got %q", result)
	}
}

func TestResolveUnknownPlaceholderStrict(t *testing.T) {
	r := placeholders.NewRegistry()
	_, err := r.Resolve("scope", "hello {unknown}", nil, nil, true)
	if err == nil {
		t.Fatal("expected error for unknown placeholder in strict mode")
	}
}

func TestResolveCustomValues(t *testing.T) {
	r := placeholders.NewRegistry()
	custom := map[string]interface{}{"name": "world"}
	result, err := r.Resolve("scope", "hello {name}", nil, custom, false)
	if err != nil {
		t.Fatal(err)
	}
	if result != "hello world" {
		t.Errorf("got %q", result)
	}
}

// ---- UnregisterScope --------------------------------------------------------

func TestUnregisterScope(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("mod1", "key1", "desc", func(_ map[string]interface{}) (string, error) {
		return "v1", nil
	})
	n := r.UnregisterScope("mod1")
	if n != 1 {
		t.Errorf("expected 1 unregistered, got %d", n)
	}
	// After unregistering, resolve should leave token as-is (non-strict).
	result, _ := r.Resolve("mod1", "{key1}", nil, nil, false)
	if result != "{key1}" {
		t.Errorf("expected unresolved token, got %q", result)
	}
}

func TestUnregisterScopeEmpty(t *testing.T) {
	r := placeholders.NewRegistry()
	n := r.UnregisterScope("nonexistent")
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

// ---- UnregisterKey ----------------------------------------------------------

func TestUnregisterKey(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("sc", "mykey", "d", func(_ map[string]interface{}) (string, error) {
		return "val", nil
	})
	ok := r.UnregisterKey("sc", "mykey")
	if !ok {
		t.Fatal("expected true")
	}
}

func TestUnregisterKeyMissing(t *testing.T) {
	r := placeholders.NewRegistry()
	ok := r.UnregisterKey("sc", "nosuchkey")
	if ok {
		t.Fatal("expected false for missing key")
	}
}

// ---- List / ListScope -------------------------------------------------------

func TestList(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("s", "a", "d", func(_ map[string]interface{}) (string, error) { return "", nil })
	r.Register("s", "b", "d", func(_ map[string]interface{}) (string, error) { return "", nil })
	l := r.List()
	if len(l) < 2 {
		t.Errorf("expected >=2 items, got %d", len(l))
	}
}

func TestListScopeFiltered(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("alpha", "x", "d", func(_ map[string]interface{}) (string, error) { return "", nil })
	r.Register("beta", "y", "d", func(_ map[string]interface{}) (string, error) { return "", nil })
	l := r.ListScope("alpha")
	for _, item := range l {
		if strings.HasPrefix(item, "beta.") {
			t.Error("ListScope should not include other scopes")
		}
	}
}

// ---- Format -----------------------------------------------------------------

func TestFormatEmpty(t *testing.T) {
	r := placeholders.NewRegistry()
	f := r.Format("empty_scope")
	if f != "" {
		t.Errorf("expected empty string for empty scope, got %q", f)
	}
}

func TestFormatNonEmpty(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("sc", "key", "a description", func(_ map[string]interface{}) (string, error) { return "", nil })
	f := r.Format("sc")
	if !strings.Contains(f, "key") {
		t.Errorf("format should mention key: %q", f)
	}
}

// ---- Options ----------------------------------------------------------------

func TestWithTimeoutOption(t *testing.T) {
	r := placeholders.NewRegistry()
	// Just ensure Register with option doesn't panic.
	r.Register("sc", "timed", "d",
		func(_ map[string]interface{}) (string, error) { return "v", nil },
		placeholders.WithTimeout(0),
	)
	result, err := r.Resolve("sc", "{timed}", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result != "v" {
		t.Errorf("got %q", result)
	}
}

func TestWithRequiredOption(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("sc", "req", "d",
		func(_ map[string]interface{}) (string, error) { return "filled", nil },
		placeholders.WithRequired,
	)
	result, err := r.Resolve("sc", "val={req}", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "filled") {
		t.Errorf("got %q", result)
	}
}

// ---- Multiple placeholders --------------------------------------------------

func TestResolveMultiplePlaceholders(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("s", "a", "d", func(_ map[string]interface{}) (string, error) { return "A", nil })
	r.Register("s", "b", "d", func(_ map[string]interface{}) (string, error) { return "B", nil })
	result, err := r.Resolve("s", "{a} and {b}", nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result != "A and B" {
		t.Errorf("got %q", result)
	}
}

func TestResolveDataOverridesGetter(t *testing.T) {
	r := placeholders.NewRegistry()
	r.Register("s", "x", "d", func(_ map[string]interface{}) (string, error) { return "from-getter", nil })
	data := map[string]interface{}{"x": "from-data"}
	result, err := r.Resolve("s", "{x}", data, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if result != "from-data" {
		t.Errorf("data should override getter, got %q", result)
	}
}

// ---- Global registry --------------------------------------------------------

func TestGlobalRegistryNotNil(t *testing.T) {
	if placeholders.Global == nil {
		t.Fatal("Global registry should not be nil")
	}
}
