package version_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/version"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestGetVersion
// ─────────────────────────────────────────────────────────────────────────────

func TestGetVersion(t *testing.T) {
	v := version.GetVersion()
	if v == "" {
		t.Fatal("GetVersion returned empty string")
	}
	// Should be a semver-like string.
	if len(v) < 3 {
		t.Errorf("version string too short: %q", v)
	}
}

func TestVersionConstant(t *testing.T) {
	if version.Version == "" {
		t.Fatal("Version constant is empty")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestDetectBranch
// ─────────────────────────────────────────────────────────────────────────────

func TestDetectBranch(t *testing.T) {
	b := version.DetectBranch()
	if b == "" {
		t.Fatal("DetectBranch returned empty string")
	}
	// In test env, git may not be available — should fall back to "main".
	// At minimum it must be a non-empty string.
	t.Logf("detected branch: %q", b)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestGetCommitSHA
// ─────────────────────────────────────────────────────────────────────────────

func TestGetCommitSHA(t *testing.T) {
	sha := version.GetCommitSHA()
	if sha == "" {
		t.Fatal("GetCommitSHA returned empty string")
	}
	// Either a 7-char hex string or "unknown".
	t.Logf("commit SHA: %q", sha)
}

// ─────────────────────────────────────────────────────────────────────────────
// TestCheckModuleCompatibility
// ─────────────────────────────────────────────────────────────────────────────

func TestCheckModuleCompatibility_NoDirective(t *testing.T) {
	ok, reason := version.CheckModuleCompatibility("# no scop directive here\nprint('hello')")
	if !ok {
		t.Errorf("expected compatible, got reason: %q", reason)
	}
}

func TestCheckModuleCompatibility_MinVersion(t *testing.T) {
	// Current version is 1.4.0; require 1.0.0 — should pass.
	code := "# scop: kernel min 1.0.0\nprint('x')"
	ok, reason := version.CheckModuleCompatibility(code)
	if !ok {
		t.Errorf("expected compatible for min 1.0.0, got: %q", reason)
	}
}

func TestCheckModuleCompatibility_MinVersionFail(t *testing.T) {
	// Require a very high version — should fail.
	code := "# scop: kernel min 99.0.0\nprint('x')"
	ok, _ := version.CheckModuleCompatibility(code)
	if ok {
		t.Error("expected incompatible for min 99.0.0")
	}
}

func TestCheckModuleCompatibility_MaxVersion(t *testing.T) {
	// Require max 99.0.0 — should pass.
	code := "# scop: kernel max 99.0.0\nprint('x')"
	ok, reason := version.CheckModuleCompatibility(code)
	if !ok {
		t.Errorf("expected compatible for max 99.0.0, got: %q", reason)
	}
}
