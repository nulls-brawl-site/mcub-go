// SPDX-License-Identifier: MIT
// Copyright (c) 2026 MCUB Go Authors

package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
)

// LogIface is the minimal logging interface satisfied by *logger.Logger.
// It is exported so that kernel and other packages can reference it in method
// signatures without importing the logger package directly.
type LogIface interface {
	Info(format string, args ...interface{})
	Warn(format string, args ...interface{})
	Error(format string, args ...interface{})
	Debug(format string, args ...interface{})
}

// systemKernelStore is the minimal kernel interface for persisting a loaded
// system module. Satisfied by *kernel.Kernel.
type systemKernelStore interface {
	StoreSystemModule(name string, m Module)
}

// SystemLoader loads built-in system modules (from modules/ directory).
// It handles:
//   - Go plugin .so files (via the standard plugin loader)
//   - Python .py system modules (via pybridge)
//   - Skips __init__.py, __pycache__ dirs, and unrecognised file types.
type SystemLoader struct {
	kernel interface{} // *kernel.Kernel – stored as interface{} to avoid import cycle
	bridge interface{} // *pybridge.Bridge – use interface{} to avoid import cycle
	loader *Loader
	log    LogIface
}

// NewSystemLoader creates a SystemLoader.
//   - kernel: the *kernel.Kernel instance (used to store loaded modules)
//   - ldr: the module Loader (provides LoadPlugin and LoadBuiltin)
//   - log: logger (any LogIface, typically *logger.Logger)
//   - bridge: the Python bridge (*pybridge.Bridge), or nil to skip .py loading
func NewSystemLoader(kernel interface{}, ldr *Loader, log LogIface, bridge interface{}) *SystemLoader {
	return &SystemLoader{
		kernel: kernel,
		bridge: bridge,
		loader: ldr,
		log:    log,
	}
}

// LoadSystemModules scans dir for .py and .so files and loads each as a
// system module. It returns the count of successfully loaded modules, a slice
// of file names that failed, and any fatal directory-level error.
func (sl *SystemLoader) LoadSystemModules(dir string) (loaded int, failed []string, err error) {
	sl.log.Info("Loading system modules from %s", dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			sl.log.Info("System modules dir %s does not exist, skipping", dir)
			return 0, nil, nil
		}
		return 0, nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // skip __pycache__ and other subdirectories
		}
		name := entry.Name()
		if name == "__init__.py" {
			continue
		}
		path := filepath.Join(dir, name)
		if loadErr := sl.LoadSystemModule(path); loadErr != nil {
			sl.log.Error("Failed to load system module %s: %v", name, loadErr)
			failed = append(failed, name)
			continue
		}
		loaded++
	}

	sl.log.Info("System modules: %d loaded, %d failed", loaded, len(failed))
	return loaded, failed, nil
}

// LoadSystemModule loads a single system module file.
//   - .py files are loaded via the Python bridge.
//   - .so files are loaded as Go plugins.
//   - All other extensions are silently skipped (not counted as failures).
func (sl *SystemLoader) LoadSystemModule(path string) error {
	name := filepath.Base(path)

	switch {
	case strings.HasSuffix(name, ".py"):
		return sl.loadPySystemModule(path)
	case strings.HasSuffix(name, ".so"):
		return sl.loader.LoadPlugin(path)
	default:
		sl.log.Debug("Skipping non-module file %s", name)
		return nil
	}
}

// loadPySystemModule loads a single .py file as a system module.
func (sl *SystemLoader) loadPySystemModule(path string) error {
	bridge, ok := sl.bridge.(*pybridge.Bridge)
	if !ok || bridge == nil {
		return fmt.Errorf("Python bridge not available")
	}

	pyMod, err := bridge.LoadPyModule(path)
	if err != nil {
		return err
	}

	pm := NewPythonModule(bridge, pyMod)

	if err := sl.loader.LoadBuiltin(pm); err != nil {
		return err
	}

	// Persist into the kernel's SystemModules map.
	if ks, ok := sl.kernel.(systemKernelStore); ok {
		ks.StoreSystemModule(pm.Name(), pm)
	}

	sl.log.Info("System module loaded: %s (commands: %d)", pm.Name(), len(pm.Commands()))
	return nil
}
