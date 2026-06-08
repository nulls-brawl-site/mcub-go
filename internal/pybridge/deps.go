// SPDX-License-Identifier: MIT
// deps.go — pip dependency installer for Python user modules.
//
// Parses # requires: metadata from module source (both MCUB and Hikka formats)
// and installs missing packages via pip with multiple fallback strategies,
// matching the logic from MCUB-fork core/lib/mixin/dependency_manager_mixin.py

package pybridge

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

// importToPip maps import names that differ from their pip package names.
// Matches _IMPORT_TO_PIP in dependency_manager_mixin.py exactly.
var importToPip = map[string]string{
	"PIL":                 "Pillow",
	"cv2":                 "opencv-python",
	"sklearn":             "scikit-learn",
	"bs4":                 "beautifulsoup4",
	"yaml":                "PyYAML",
	"dotenv":              "python-dotenv",
	"google.generativeai": "google-generativeai",
	"speech_recognition":  "SpeechRecognition",
	"dateutil":            "python-dateutil",
	"Crypto":              "pycryptodome",
	"usb":                 "pyusb",
	"gi":                  "PyGObject",
	"wx":                  "wxPython",
	"Image":               "Pillow",
	"pkg_resources":       "setuptools",
}

// nonInstallable is the set of stdlib modules that must never be pip-installed.
// Matches _NON_INSTALLABLE in dependency_manager_mixin.py exactly.
var nonInstallable = map[string]bool{
	"os": true, "sys": true, "re": true, "io": true, "math": true,
	"time": true, "json": true, "uuid": true, "html": true, "http": true,
	"urllib": true, "email": true, "logging": true, "hashlib": true,
	"hmac": true, "base64": true, "struct": true, "socket": true,
	"ssl": true, "threading": true, "multiprocessing": true, "asyncio": true,
	"inspect": true, "traceback": true, "importlib": true, "pathlib": true,
	"shutil": true, "tempfile": true, "glob": true, "fnmatch": true,
	"collections": true, "itertools": true, "functools": true, "operator": true,
	"copy": true, "pprint": true, "textwrap": true, "string": true,
	"enum": true, "typing": true, "dataclasses": true, "abc": true,
	"contextlib": true, "warnings": true, "weakref": true, "gc": true,
	"random": true, "statistics": true, "decimal": true, "fractions": true,
	"datetime": true, "calendar": true, "zlib": true, "gzip": true,
	"bz2": true, "lzma": true, "zipfile": true, "tarfile": true,
	"csv": true, "sqlite3": true, "xml": true, "ftplib": true,
	"imaplib": true, "smtplib": true, "unittest": true, "doctest": true,
	"pdb": true, "profile": true, "timeit": true, "signal": true,
	"platform": true, "sysconfig": true, "site": true, "builtins": true,
	"tokenize": true, "ast": true, "dis": true, "code": true,
	"codeop": true, "compileall": true, "py_compile": true,
	// Additional commonly-imported stdlib/bridge modules
	"types": true, "subprocess": true, "ctypes": true, "array": true,
	"queue": true, "heapq": true, "bisect": true, "mmap": true,
	"aiohttp": false, // aiohttp IS installable — listed here as placeholder
}

// reRequires parses `# requires: pkg1 pkg2` lines.
// Also handles `# requires: pkg1, pkg2` (comma-separated) — Hikka style.
var reRequires = regexp.MustCompile(`(?im)^\s*#\s*requires\s*:\s*(.+)$`)

// reScopePip parses `# scope: pip pkg1 pkg2` lines (Hikka extended format).
var reScopePip = regexp.MustCompile(`(?im)#\s*scope\s*:\s*pip\s+((?:[A-Za-z0-9\-_>=<!\[\].]+(?:\s+|$))+)`)

// reVersionSpec strips version specifiers from a package name: `pkg>=1.0` → `pkg`.
var reVersionSpec = regexp.MustCompile(`[<>=!~\[].*`)

// ParseRequires extracts pip package names from Python module source.
// Supports:
//   - MCUB format:  `# requires: aiohttp Pillow`
//   - Hikka format: `# requires: aiohttp, Pillow`  (comma-separated)
//   - Hikka scope:  `# scope: pip aiohttp Pillow`
func ParseRequires(src string) []string {
	seen := map[string]bool{}
	var result []string

	add := func(pkg string) {
		pkg = strings.TrimSpace(pkg)
		if pkg == "" || pkg == "requires:" {
			return
		}
		// strip version specifiers to get the importable name
		bare := reVersionSpec.ReplaceAllString(pkg, "")
		bare = strings.TrimSpace(bare)
		if bare == "" {
			return
		}
		if !seen[bare] {
			seen[bare] = true
			result = append(result, pkg) // keep full spec for pip
		}
	}

	// # requires: ...
	for _, m := range reRequires.FindAllStringSubmatch(src, -1) {
		raw := m[1]
		// split on whitespace and/or commas
		for _, tok := range regexp.MustCompile(`[,\s]+`).Split(raw, -1) {
			add(tok)
		}
	}

	// # scope: pip ...
	for _, m := range reScopePip.FindAllStringSubmatch(src, -1) {
		raw := strings.TrimSpace(m[1])
		for _, tok := range strings.Fields(raw) {
			add(tok)
		}
	}

	return result
}

// resolvePipName converts an import name to its pip package name.
func resolvePipName(importName string) string {
	base := reVersionSpec.ReplaceAllString(importName, "")
	if pip, ok := importToPip[base]; ok {
		return strings.Replace(importName, base, pip, 1)
	}
	return importName
}

// isNonInstallable returns true if the package is part of the stdlib.
func isNonInstallable(pkg string) bool {
	base := reVersionSpec.ReplaceAllString(pkg, "")
	base = strings.ToLower(strings.TrimSpace(base))
	// Only exact stdlib names — avoid blocking real packages
	for k := range nonInstallable {
		if strings.ToLower(k) == base && nonInstallable[k] {
			return true
		}
	}
	return false
}

// pythonExec returns the path to the Python executable.
func pythonExec() string {
	for _, name := range []string{"python3", "python"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return "python3"
}

// isVirtualEnv returns true if running inside a virtualenv / venv.
func isVirtualEnv() bool {
	py := pythonExec()
	out, err := exec.Command(py, "-c",
		"import sys; print(hasattr(sys,'real_prefix') or (hasattr(sys,'base_prefix') and sys.base_prefix!=sys.prefix))").
		Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "True"
}

// isInstalled checks whether a Python package is importable.
func isInstalled(ctx context.Context, importName string) bool {
	base := reVersionSpec.ReplaceAllString(importName, "")
	base = strings.TrimFunc(base, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' })
	if base == "" {
		return true
	}
	py := pythonExec()
	cmd := exec.CommandContext(ctx, py, "-c", fmt.Sprintf("import importlib.util; exit(0 if importlib.util.find_spec(%q) else 1)", base))
	return cmd.Run() == nil
}

// PipInstall installs a single pip package using multiple fallback strategies.
// Matches _pip_install in dependency_manager_mixin.py exactly.
func PipInstall(ctx context.Context, pipSpec string) error {
	py := pythonExec()

	type strategy struct {
		args []string
		desc string
	}

	var strategies []strategy

	// --break-system-packages first when not in a venv (matches Python logic)
	if !isVirtualEnv() {
		strategies = append(strategies, strategy{
			args: []string{py, "-m", "pip", "install", pipSpec, "--break-system-packages"},
			desc: "pip --break-system-packages",
		})
	}

	strategies = append(strategies,
		strategy{[]string{py, "-m", "pip", "install", pipSpec}, "pip"},
		strategy{[]string{py, "-m", "pip3", "install", pipSpec}, "pip3 module"},
	)

	// system pip / pip3 binaries
	if pipPath, err := exec.LookPath("pip"); err == nil {
		strategies = append(strategies, strategy{[]string{pipPath, "install", pipSpec}, "pip bin"})
	}
	if pip3Path, err := exec.LookPath("pip3"); err == nil {
		strategies = append(strategies, strategy{[]string{pip3Path, "install", pipSpec}, "pip3 bin"})
	}

	// On Windows also try py -m pip
	if runtime.GOOS == "windows" {
		strategies = append(strategies, strategy{[]string{"py", "-m", "pip", "install", pipSpec}, "py -m pip"})
	}

	var lastErr error
	for _, s := range strategies {
		cmd := exec.CommandContext(ctx, s.args[0], s.args[1:]...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("strategy %q failed: %s", s.desc, strings.TrimSpace(string(out)))
	}
	return fmt.Errorf("pip install %q: all strategies failed: %w", pipSpec, lastErr)
}

// InstallRequires parses `# requires:` from src and installs any missing packages.
// moduleName is used for log messages only.
// Returns a list of (package, error) pairs for packages that failed to install.
func InstallRequires(ctx context.Context, src string, moduleName string, logFn func(string)) []error {
	reqs := ParseRequires(src)
	if len(reqs) == 0 {
		return nil
	}

	// filter stdlib
	var toInstall []string
	for _, req := range reqs {
		if isNonInstallable(req) {
			continue
		}
		toInstall = append(toInstall, req)
	}
	if len(toInstall) == 0 {
		return nil
	}

	if logFn != nil {
		logFn(fmt.Sprintf("[%s] dependencies: %v", moduleName, toInstall))
	}

	// install missing concurrently
	var mu sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	for _, req := range toInstall {
		req := req
		wg.Add(1)
		go func() {
			defer wg.Done()

			base := reVersionSpec.ReplaceAllString(req, "")
			if isInstalled(ctx, base) {
				if logFn != nil {
					logFn(fmt.Sprintf("[%s] %s already installed", moduleName, base))
				}
				return
			}

			pipSpec := resolvePipName(req)
			if logFn != nil {
				logFn(fmt.Sprintf("[%s] installing %s", moduleName, pipSpec))
			}

			if err := PipInstall(ctx, pipSpec); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", pipSpec, err))
				mu.Unlock()
				if logFn != nil {
					logFn(fmt.Sprintf("[%s] failed to install %s: %v", moduleName, pipSpec, err))
				}
			} else {
				if logFn != nil {
					logFn(fmt.Sprintf("[%s] installed %s", moduleName, pipSpec))
				}
			}
		}()
	}

	wg.Wait()
	return errs
}
