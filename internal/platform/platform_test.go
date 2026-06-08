package platform_test

import (
	"runtime"
	"strings"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/platform"
)

// ---- GetPlatform ------------------------------------------------------------

func TestGetPlatformNotEmpty(t *testing.T) {
	p := platform.GetPlatform()
	if p == "" {
		t.Fatal("empty platform")
	}
}

func TestGetPlatformKnownValues(t *testing.T) {
	p := platform.GetPlatform()
	allowed := map[string]bool{
		"termux": true, "wsl": true, "wsl2": true,
		"docker": true, "linux": true, "darwin": true,
		"windows": true, "unknown": true,
	}
	if !allowed[p] {
		t.Errorf("unexpected platform value %q", p)
	}
}

func TestGetPlatformMatchesGOOS(t *testing.T) {
	p := platform.GetPlatform()
	// On a standard non-Termux/WSL/Docker system, the platform should
	// match runtime.GOOS (linux/darwin/windows) or be a sub-type.
	goos := runtime.GOOS
	switch goos {
	case "linux":
		allowed := map[string]bool{"linux": true, "docker": true, "wsl": true, "wsl2": true, "termux": true}
		if !allowed[p] {
			t.Errorf("on linux GOOS expected linux-family platform, got %q", p)
		}
	case "darwin":
		if p != "darwin" {
			t.Errorf("on darwin expected 'darwin', got %q", p)
		}
	case "windows":
		if p != "windows" {
			t.Errorf("on windows expected 'windows', got %q", p)
		}
	}
}

// ---- GetArch ----------------------------------------------------------------

func TestGetArchNotEmpty(t *testing.T) {
	if platform.GetArch() == "" {
		t.Fatal("empty arch")
	}
}

func TestGetArchMatchesRuntime(t *testing.T) {
	if platform.GetArch() != runtime.GOARCH {
		t.Errorf("GetArch() = %q, want %q", platform.GetArch(), runtime.GOARCH)
	}
}

// ---- GetGoVersion -----------------------------------------------------------

func TestGetGoVersionNotEmpty(t *testing.T) {
	if platform.GetGoVersion() == "" {
		t.Fatal("empty go version")
	}
}

func TestGetGoVersionPrefix(t *testing.T) {
	v := platform.GetGoVersion()
	if !strings.HasPrefix(v, "go") {
		t.Errorf("expected 'go' prefix, got %q", v)
	}
}

// ---- Detect -----------------------------------------------------------------

func TestDetectFields(t *testing.T) {
	info := platform.Detect()
	if info.Platform == "" {
		t.Error("Platform is empty")
	}
	if info.Arch == "" {
		t.Error("Arch is empty")
	}
	if info.GoVersion == "" {
		t.Error("GoVersion is empty")
	}
	if info.GOOS == "" {
		t.Error("GOOS is empty")
	}
}

func TestDetectGOOS(t *testing.T) {
	info := platform.Detect()
	if info.GOOS != runtime.GOOS {
		t.Errorf("GOOS mismatch: %q != %q", info.GOOS, runtime.GOOS)
	}
}

// ---- FriendlyName -----------------------------------------------------------

func TestFriendlyNameNotEmpty(t *testing.T) {
	name := platform.FriendlyName()
	if name == "" {
		t.Fatal("empty friendly name")
	}
}

// ---- IsTermux / IsWSL / IsDocker --------------------------------------------

func TestIsTermuxBool(t *testing.T) {
	// Just assert it returns without panic.
	_ = platform.IsTermux()
}

func TestIsWSLBool(t *testing.T) {
	_ = platform.IsWSL()
}

func TestIsDockerBool(t *testing.T) {
	_ = platform.IsDocker()
}

// ---- GetDistroName / GetOSVersion -------------------------------------------

func TestGetDistroNameString(t *testing.T) {
	// May be empty on non-Linux; just must not panic.
	_ = platform.GetDistroName()
}

func TestGetOSVersionString(t *testing.T) {
	// May return empty on some systems; just must not panic.
	_ = platform.GetOSVersion()
}
