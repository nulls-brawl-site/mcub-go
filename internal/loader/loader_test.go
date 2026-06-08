package loader_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/loader"
)

// mockModule is a minimal loader.Module implementation used in tests.
type mockModule struct {
	name     string
	loaded   bool
	commands []loader.Command
}

func (m *mockModule) Name() string { return m.name }
func (m *mockModule) OnLoad(_ interface{}) error {
	m.loaded = true
	return nil
}
func (m *mockModule) OnUnload(_ interface{}) error {
	m.loaded = false
	return nil
}
func (m *mockModule) Commands() []loader.Command { return m.commands }

// ---------------------------------------------------------------------------

func TestLoadBuiltin(t *testing.T) {
	l := loader.NewLoader(nil)
	mod := &mockModule{name: "test"}
	if err := l.LoadBuiltin(mod); err != nil {
		t.Fatalf("LoadBuiltin: unexpected error: %v", err)
	}
	if !l.Registry().Has("test") {
		t.Fatal("expected module to be in registry after LoadBuiltin")
	}
	if !mod.loaded {
		t.Fatal("expected OnLoad to have been called")
	}
}

func TestLoadBuiltinDuplicate(t *testing.T) {
	l := loader.NewLoader(nil)
	mod := &mockModule{name: "dup"}
	if err := l.LoadBuiltin(mod); err != nil {
		t.Fatalf("first LoadBuiltin failed unexpectedly: %v", err)
	}
	// Second registration with the same name must return an error.
	if err := l.LoadBuiltin(&mockModule{name: "dup"}); err == nil {
		t.Fatal("expected error on duplicate LoadBuiltin, got nil")
	}
}

func TestUnload(t *testing.T) {
	l := loader.NewLoader(nil)
	mod := &mockModule{name: "unload_me"}
	if err := l.LoadBuiltin(mod); err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}
	if err := l.Unload("unload_me"); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	if l.Registry().Has("unload_me") {
		t.Fatal("expected module to be removed from registry after Unload")
	}
	if mod.loaded {
		t.Fatal("expected OnUnload to have been called")
	}
}

func TestUnloadNotFound(t *testing.T) {
	l := loader.NewLoader(nil)
	if err := l.Unload("nonexistent"); err == nil {
		t.Fatal("expected error when unloading non-existent module, got nil")
	}
}

func TestAllCommands(t *testing.T) {
	l := loader.NewLoader(nil)
	cmds := []loader.Command{
		{Name: "cmd1", Description: "first"},
		{Name: "cmd2", Description: "second"},
	}
	mod := &mockModule{name: "multi", commands: cmds}
	if err := l.LoadBuiltin(mod); err != nil {
		t.Fatalf("LoadBuiltin: %v", err)
	}

	all := l.AllCommands()
	if len(all) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(all))
	}
	names := make(map[string]bool)
	for _, c := range all {
		names[c.Name] = true
	}
	for _, expected := range []string{"cmd1", "cmd2"} {
		if !names[expected] {
			t.Errorf("command %q missing from AllCommands()", expected)
		}
	}
}

func TestAllCommandsMultipleModules(t *testing.T) {
	l := loader.NewLoader(nil)

	modA := &mockModule{name: "modA", commands: []loader.Command{{Name: "a"}}}
	modB := &mockModule{name: "modB", commands: []loader.Command{{Name: "b"}, {Name: "c"}}}

	if err := l.LoadBuiltin(modA); err != nil {
		t.Fatalf("LoadBuiltin modA: %v", err)
	}
	if err := l.LoadBuiltin(modB); err != nil {
		t.Fatalf("LoadBuiltin modB: %v", err)
	}

	all := l.AllCommands()
	if len(all) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(all))
	}
}

func TestRegistryGet(t *testing.T) {
	l := loader.NewLoader(nil)
	mod := &mockModule{name: "getme"}
	_ = l.LoadBuiltin(mod)

	got, ok := l.Registry().Get("getme")
	if !ok {
		t.Fatal("Get returned false for registered module")
	}
	if got.Name() != "getme" {
		t.Fatalf("Get returned wrong module name: %q", got.Name())
	}
}

func TestRegistrySource(t *testing.T) {
	l := loader.NewLoader(nil)
	_ = l.LoadBuiltin(&mockModule{name: "src_test"})

	src, ok := l.Registry().Source("src_test")
	if !ok {
		t.Fatal("Source returned false for registered module")
	}
	if src != loader.ModuleSourceBuiltin {
		t.Fatalf("expected builtin source, got %q", src)
	}
}
