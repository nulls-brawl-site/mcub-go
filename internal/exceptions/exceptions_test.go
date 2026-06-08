package exceptions_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/exceptions"
)

// ---- CommandConflictError ---------------------------------------------------

func TestCommandConflictErrorMessage(t *testing.T) {
	err := &exceptions.CommandConflictError{Command: "ping", ConflictType: "system"}
	if err.Error() == "" {
		t.Fatal("empty error message")
	}
	if !strings.Contains(err.Error(), "ping") {
		t.Errorf("error should mention command: %q", err.Error())
	}
}

func TestCommandConflictErrorWithModule(t *testing.T) {
	err := &exceptions.CommandConflictError{Command: "kick", ConflictType: "user", Module: "admin"}
	msg := err.Error()
	if !strings.Contains(msg, "admin") {
		t.Errorf("error should mention module: %q", msg)
	}
	if !strings.Contains(msg, "kick") {
		t.Errorf("error should mention command: %q", msg)
	}
}

func TestIsCommandConflict(t *testing.T) {
	err := &exceptions.CommandConflictError{Command: "ping", ConflictType: "system"}
	if !exceptions.IsCommandConflict(err) {
		t.Fatal("should detect conflict")
	}
}

func TestIsCommandConflictFalse(t *testing.T) {
	if exceptions.IsCommandConflict(errors.New("other error")) {
		t.Fatal("should not detect conflict for generic error")
	}
}

func TestIsCommandConflictWrapped(t *testing.T) {
	inner := &exceptions.CommandConflictError{Command: "x", ConflictType: "system"}
	wrapped := fmt.Errorf("wrap: %w", inner)
	if !exceptions.IsCommandConflict(wrapped) {
		t.Fatal("should detect wrapped conflict")
	}
}

// ---- CallInsecure -----------------------------------------------------------

func TestCallInsecureMessage(t *testing.T) {
	err := &exceptions.CallInsecure{Name: "_kernel", ModuleName: "badmod"}
	msg := err.Error()
	if msg == "" {
		t.Fatal("empty error message")
	}
	if !strings.Contains(msg, "_kernel") {
		t.Errorf("message should mention name: %q", msg)
	}
	if !strings.Contains(msg, "badmod") {
		t.Errorf("message should mention module: %q", msg)
	}
}

func TestCallInsecureNoModule(t *testing.T) {
	err := &exceptions.CallInsecure{Name: "_secret"}
	if err.Error() == "" {
		t.Fatal("empty error")
	}
}

func TestIsCallInsecure(t *testing.T) {
	err := &exceptions.CallInsecure{Name: "x"}
	if !exceptions.IsCallInsecure(err) {
		t.Fatal("should detect insecure call")
	}
}

func TestIsCallInsecureFalse(t *testing.T) {
	if exceptions.IsCallInsecure(errors.New("other")) {
		t.Fatal("generic error should not be insecure call")
	}
}

// ---- ModuleLoadError --------------------------------------------------------

func TestModuleLoadErrorMessage(t *testing.T) {
	err := &exceptions.ModuleLoadError{ModuleName: "mymod"}
	msg := err.Error()
	if !strings.Contains(msg, "mymod") {
		t.Errorf("expected module name in error: %q", msg)
	}
}

func TestModuleLoadErrorWithCause(t *testing.T) {
	cause := errors.New("syntax error")
	err := &exceptions.ModuleLoadError{ModuleName: "badmod", Cause: cause}
	if !strings.Contains(err.Error(), "syntax error") {
		t.Errorf("expected cause in error: %q", err.Error())
	}
}

func TestModuleLoadErrorUnwrap(t *testing.T) {
	cause := errors.New("root cause")
	err := &exceptions.ModuleLoadError{Cause: cause}
	if !errors.Is(err, cause) {
		t.Fatal("Unwrap should expose cause")
	}
}

func TestIsModuleLoad(t *testing.T) {
	err := &exceptions.ModuleLoadError{ModuleName: "x"}
	if !exceptions.IsModuleLoad(err) {
		t.Fatal("should detect module load error")
	}
}

func TestIsModuleLoadFalse(t *testing.T) {
	if exceptions.IsModuleLoad(errors.New("other")) {
		t.Fatal("generic error should not match")
	}
}

// ---- ScamModuleDetected -----------------------------------------------------

func TestScamModuleDetectedMessage(t *testing.T) {
	err := &exceptions.ScamModuleDetected{Method: "steal_token"}
	msg := err.Error()
	if !strings.Contains(msg, "steal_token") {
		t.Errorf("message should mention method: %q", msg)
	}
}

// ---- McubTelethonError ------------------------------------------------------

func TestMcubTelethonErrorDefault(t *testing.T) {
	err := &exceptions.McubTelethonError{}
	if err.Error() == "" {
		t.Fatal("empty error")
	}
}

func TestMcubTelethonErrorCustom(t *testing.T) {
	err := &exceptions.McubTelethonError{Message: "custom message"}
	if err.Error() != "custom message" {
		t.Errorf("got %q", err.Error())
	}
}
