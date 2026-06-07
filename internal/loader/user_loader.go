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

// userKernelStore is the minimal kernel interface for persisting a loaded
// user module. Satisfied by *kernel.Kernel.
type userKernelStore interface {
	StoreUserModule(name string, m Module)
}

// kernelDepsIface is satisfied by *kernel.Kernel and used by the package-level
// LoadUserModules helper to extract the dependencies it needs.
type kernelDepsIface interface {
	GetLoader() *Loader
	GetLogIface() LogIface
	GetBridge() interface{}
}

// UserLoader loads user-installed modules (from modules_loaded/ directory).
// Same as SystemLoader but marks modules as ModuleSourceUser and stores them
// in the kernel's LoadedModules map. Supports:
//   - Python .py files (via pybridge)
//   - Go plugin .so files (via the standard plugin loader)
type UserLoader struct {
	kernel interface{} // *kernel.Kernel – stored as interface{} to avoid import cycle
	bridge interface{} // *pybridge.Bridge – use interface{} to avoid import cycle
	loader *Loader
	log    LogIface
}

// NewUserLoader creates a UserLoader.
//   - kernel: the *kernel.Kernel instance (used to store loaded modules)
//   - ldr: the module Loader
//   - log: logger
//   - bridge: the Python bridge (*pybridge.Bridge), or nil to skip .py loading
func NewUserLoader(kernel interface{}, ldr *Loader, log LogIface, bridge interface{}) *UserLoader {
	return &UserLoader{
		kernel: kernel,
		bridge: bridge,
		loader: ldr,
		log:    log,
	}
}

// LoadUserModules scans dir for .py and .so files and loads each as a
// user-sourced module. It returns the count of successfully loaded modules,
// a slice of file names that failed, and any fatal directory-level error.
func (ul *UserLoader) LoadUserModules(dir string) (loaded int, failed []string, err error) {
	ul.log.Info("Loading user modules from %s", dir)

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			ul.log.Info("User modules dir %s does not exist, skipping", dir)
			return 0, nil, nil
		}
		return 0, nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // skip subdirectories
		}
		name := entry.Name()
		if name == "__init__.py" {
			continue
		}
		path := filepath.Join(dir, name)
		if loadErr := ul.loadUserModule(path); loadErr != nil {
			ul.log.Error("Failed to load user module %s: %v", name, loadErr)
			failed = append(failed, name)
			continue
		}
		loaded++
	}

	ul.log.Info("User modules: %d loaded, %d failed", loaded, len(failed))
	return loaded, failed, nil
}

// loadUserModule dispatches to the correct loader based on file extension.
func (ul *UserLoader) loadUserModule(path string) error {
	name := filepath.Base(path)

	switch {
	case strings.HasSuffix(name, ".py"):
		return ul.loadPyUserModule(path)
	case strings.HasSuffix(name, ".so"):
		return ul.loader.LoadPlugin(path)
	default:
		ul.log.Debug("Skipping non-module file %s", name)
		return nil
	}
}

// loadPyUserModule loads a single .py file as a user module.
// Unlike system modules (which use LoadBuiltin → ModuleSourceBuiltin), user
// modules are registered with ModuleSourceUser.
func (ul *UserLoader) loadPyUserModule(path string) error {
	bridge, ok := ul.bridge.(*pybridge.Bridge)
	if !ok || bridge == nil {
		return fmt.Errorf("Python bridge not available")
	}

	pyMod, err := bridge.LoadPyModule(path)
	if err != nil {
		return err
	}

	pm := NewPythonModule(bridge, pyMod)

	// Register with user source tag (not ModuleSourceBuiltin).
	if err := ul.loader.registry.Register(pm, ModuleSourceUser); err != nil {
		return err
	}
	if err := pm.OnLoad(ul.kernel); err != nil {
		_ = ul.loader.registry.Unregister(pm.Name())
		return fmt.Errorf("OnLoad %q: %w", pm.Name(), err)
	}

	// Persist into the kernel's LoadedModules map.
	if ks, ok := ul.kernel.(userKernelStore); ok {
		ks.StoreUserModule(pm.Name(), pm)
	}

	ul.log.Info("User module loaded: %s (commands: %d)", pm.Name(), len(pm.Commands()))
	return nil
}

// LoadUserModules is the package-level convenience wrapper. It extracts the
// required dependencies from kernel (which must satisfy kernelDepsIface) and
// delegates to a UserLoader.
//
// This matches the function signature requested by the task spec:
//
//	LoadUserModules(kernel interface{}, dir string) (loaded int, failed []string, err error)
func LoadUserModules(kernel interface{}, dir string) (loaded int, failed []string, err error) {
	kd, ok := kernel.(kernelDepsIface)
	if !ok {
		return 0, nil, fmt.Errorf("LoadUserModules: kernel does not implement kernelDepsIface (missing GetLoader/GetLogIface/GetBridge)")
	}
	ul := NewUserLoader(kernel, kd.GetLoader(), kd.GetLogIface(), kd.GetBridge())
	return ul.LoadUserModules(dir)
}
