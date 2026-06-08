package modules_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/modules"
)

// TestPaymentsModuleNotNil verifies that newPaymentsModule (registered via
// AllSystemModules) returns a non-nil Module.
func TestPaymentsModuleNotNil(t *testing.T) {
	var found bool
	for _, m := range modules.AllSystemModules() {
		if m.Name() == "payments" {
			found = true
			if m == nil {
				t.Fatal("payments module is nil")
			}
			break
		}
	}
	if !found {
		t.Fatal("payments module not found in AllSystemModules()")
	}
}

// TestPaymentsModuleName verifies that the payments module reports the correct name.
func TestPaymentsModuleName(t *testing.T) {
	for _, m := range modules.AllSystemModules() {
		if m.Name() == "payments" {
			if got := m.Name(); got != "payments" {
				t.Errorf("payments module Name() = %q, want %q", got, "payments")
			}
			return
		}
	}
	t.Fatal("payments module not found in AllSystemModules()")
}

// TestPaymentsModuleCommands verifies that Commands() returns at least the three
// required commands: "stars", "starshistory", "stargifts".
func TestPaymentsModuleCommands(t *testing.T) {
	required := []string{"stars", "starshistory", "stargifts"}

	for _, m := range modules.AllSystemModules() {
		if m.Name() != "payments" {
			continue
		}

		cmds := m.Commands()
		if len(cmds) < 3 {
			t.Errorf("payments Commands() returned %d command(s), want at least 3", len(cmds))
		}

		names := make(map[string]bool, len(cmds))
		for _, c := range cmds {
			names[c.Name] = true
		}
		for _, r := range required {
			if !names[r] {
				t.Errorf("payments Commands(): missing command %q", r)
			}
		}
		return
	}
	t.Fatal("payments module not found in AllSystemModules()")
}

// TestPaymentsModuleOnLoadWrongType verifies that passing a wrong type to OnLoad
// returns an error (not a panic).
func TestPaymentsModuleOnLoadWrongType(t *testing.T) {
	for _, m := range modules.AllSystemModules() {
		if m.Name() != "payments" {
			continue
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("payments OnLoad(\"wrong\") panicked: %v", r)
			}
		}()

		err := m.OnLoad("wrong type")
		if err == nil {
			t.Error("payments OnLoad(\"wrong type\") returned nil error, want non-nil")
		}
		return
	}
	t.Fatal("payments module not found in AllSystemModules()")
}

// TestPaymentsModuleOnUnloadWhenNotLoaded verifies that OnUnload on a freshly
// constructed (never-loaded) module returns nil.
func TestPaymentsModuleOnUnloadWhenNotLoaded(t *testing.T) {
	for _, m := range modules.AllSystemModules() {
		if m.Name() != "payments" {
			continue
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("payments OnUnload(nil) panicked: %v", r)
			}
		}()

		// Module has never had OnLoad called, so internal kernel pointer is nil.
		// OnUnload should return nil gracefully.
		err := m.OnUnload(nil)
		if err != nil {
			t.Errorf("payments OnUnload(nil) = %v, want nil", err)
		}
		return
	}
	t.Fatal("payments module not found in AllSystemModules()")
}
