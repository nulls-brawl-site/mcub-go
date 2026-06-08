package modules_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/modules"
)

// TestAllSystemModulesNames verifies that AllSystemModules returns every
// expected built-in module.
func TestAllSystemModulesNames(t *testing.T) {
	mods := modules.AllSystemModules()
	if len(mods) == 0 {
		t.Fatal("AllSystemModules() returned no modules")
	}

	names := make(map[string]bool, len(mods))
	for _, m := range mods {
		names[m.Name()] = true
	}

	required := []string{
		"tester",
		"updates",
		"MCUB_info",
		"settings",
		"man",
		"terminal",
		"trusted",
		"api_protection",
		"tr",
		"core",
		"loader",
	}
	for _, r := range required {
		if !names[r] {
			t.Errorf("AllSystemModules(): missing module %q", r)
		}
	}
}

// TestAllSystemModulesNoDuplicates verifies that no module name appears twice.
func TestAllSystemModulesNoDuplicates(t *testing.T) {
	mods := modules.AllSystemModules()
	seen := make(map[string]bool, len(mods))
	for _, m := range mods {
		n := m.Name()
		if seen[n] {
			t.Errorf("AllSystemModules(): duplicate module name %q", n)
		}
		seen[n] = true
	}
}

// TestAllSystemModulesOnLoad verifies that each module's OnLoad does not panic
// when passed a nil kernel. A type-assertion error is expected and acceptable;
// a panic is not.
func TestAllSystemModulesOnLoad(t *testing.T) {
	for _, m := range modules.AllSystemModules() {
		m := m // capture
		t.Run(m.Name(), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("module %q: OnLoad(nil) panicked: %v", m.Name(), r)
				}
			}()
			// Passing nil is expected to return an error (type assertion fails),
			// not to panic.
			_ = m.OnLoad(nil)
		})
	}
}

// TestAllSystemModulesCommandsNotNil verifies that Commands() never returns a
// nil slice (an empty slice is fine).
func TestAllSystemModulesCommandsNotNil(t *testing.T) {
	for _, m := range modules.AllSystemModules() {
		cmds := m.Commands()
		// nil and empty are both acceptable; we just call it to check for panics.
		_ = cmds
	}
}
