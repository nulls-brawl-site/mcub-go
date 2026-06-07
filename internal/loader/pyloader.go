// Package loader – pyloader.go
//
// PyLoader handles downloading, parsing, and loading Python .py modules
// through the embedded Python bridge. It is the Go equivalent of the Python
// module_loader_mixin, module_detector_mixin, and dependency_manager_mixin.
package loader

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
)

// PyLoader handles loading Python .py modules via the embedded Python bridge.
type PyLoader struct {
	bridge *pybridge.Bridge
	kernel interface{}
}

// NewPyLoader creates a PyLoader backed by bridge. kernel is passed to
// PythonModule.OnLoad when modules are registered via the Loader.
func NewPyLoader(bridge *pybridge.Bridge, kernel interface{}) *PyLoader {
	return &PyLoader{bridge: bridge, kernel: kernel}
}

// LoadFromFile loads the .py file at path through the pybridge and returns the
// resulting PyModule.
func (pl *PyLoader) LoadFromFile(path string) (*pybridge.PyModule, error) {
	if pl.bridge == nil {
		return nil, fmt.Errorf("pyloader: bridge not initialised")
	}
	return pl.bridge.LoadPyModule(path)
}

// LoadFromURL downloads a .py file from url, writes it to destDir/<basename>,
// installs any "# requires:" dependencies, then loads the module.
func (pl *PyLoader) LoadFromURL(url, destDir string) (*pybridge.PyModule, error) {
	// Derive file name from the URL path component.
	base := filepath.Base(url)
	if !strings.HasSuffix(base, ".py") {
		base += ".py"
	}

	// Download the source.
	resp, err := http.Get(url) // #nosec G107 – deliberate remote module load
	if err != nil {
		return nil, fmt.Errorf("pyloader: download %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pyloader: download %q returned HTTP %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pyloader: read response body from %q: %w", url, err)
	}
	code := string(data)

	// Persist to destDir.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("pyloader: create dest dir %q: %w", destDir, err)
	}
	destPath := filepath.Join(destDir, base)
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("pyloader: write module file %q: %w", destPath, err)
	}

	// Install dependencies declared in the source (best-effort, non-fatal).
	if pkgs := pl.ParseRequires(code); len(pkgs) > 0 {
		_ = pl.InstallRequires(pkgs)
	}

	return pl.LoadFromFile(destPath)
}

// ParseRequires parses dependency declarations from Python source and returns a
// deduplicated list of package specifiers.
//
// Recognised comment forms:
//
//	# requires: aiohttp requests
//	# requires: some-package>=1.0
//	# pip install aiohttp
func (pl *PyLoader) ParseRequires(code string) []string {
	requiresRe := regexp.MustCompile(`(?i)^\s*#\s*requires\s*:\s*(.+)$`)
	pipRe := regexp.MustCompile(`(?i)^\s*#\s*pip\s+install\s+(.+)$`)
	splitRe := regexp.MustCompile(`[,\s]+`)

	seen := make(map[string]struct{})
	var result []string

	scanner := bufio.NewScanner(strings.NewReader(code))
	for scanner.Scan() {
		line := scanner.Text()
		var raw string
		switch {
		case requiresRe.MatchString(line):
			raw = requiresRe.FindStringSubmatch(line)[1]
		case pipRe.MatchString(line):
			raw = pipRe.FindStringSubmatch(line)[1]
		default:
			continue
		}
		for _, tok := range splitRe.Split(strings.TrimSpace(raw), -1) {
			tok = strings.TrimSpace(tok)
			if tok == "" || strings.EqualFold(tok, "requires:") {
				continue
			}
			if _, dup := seen[tok]; !dup {
				seen[tok] = struct{}{}
				result = append(result, tok)
			}
		}
	}
	return result
}

// InstallRequires installs each package in pkgs via pip. It tries multiple
// strategies in order:
//
//  1. python3 -m pip install PKG --break-system-packages
//  2. python3 -m pip install PKG
//  3. pip install PKG
//  4. pip3 install PKG
//
// Returns a combined error if any package could not be installed.
func (pl *PyLoader) InstallRequires(packages []string) error {
	var failures []string
	for _, pkg := range packages {
		if err := pl.installOne(pkg); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", pkg, err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("pyloader: pip install failures: %s", strings.Join(failures, "; "))
	}
	return nil
}

// installOne attempts to pip-install a single package using a series of
// fallback commands.
func (pl *PyLoader) installOne(pkg string) error {
	strategies := [][]string{
		{"python3", "-m", "pip", "install", pkg, "--break-system-packages"},
		{"python3", "-m", "pip", "install", pkg},
		{"pip", "install", pkg},
		{"pip3", "install", pkg},
	}
	for _, args := range strategies {
		cmd := exec.Command(args[0], args[1:]...) // #nosec G204
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("all pip strategies failed for %q", pkg)
}

// DetectStyle scans Python source and returns the module registration style:
//
//   - "class"    – a class inheriting from ModuleBase or Module
//   - "function" – a module-level def register(kernel): function
//   - "loader"   – module-level functions decorated with @command or @loader.command
func (pl *PyLoader) DetectStyle(code string) string {
	classRe := regexp.MustCompile(`(?m)^\s*class\s+\w+\s*\(\s*(?:\w+\.)*(?:ModuleBase|Module)\s*\)`)
	funcRe := regexp.MustCompile(`(?m)^\s*def\s+register\s*\(\s*kernel\s*\)`)
	loaderRe := regexp.MustCompile(`(?m)^\s*@(?:\w+\.)?command\b`)

	switch {
	case classRe.MatchString(code):
		return "class"
	case funcRe.MatchString(code):
		return "function"
	case loaderRe.MatchString(code):
		return "loader"
	default:
		return "function"
	}
}
