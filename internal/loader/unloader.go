// Package loader – unloader.go
//
// Provides full module unload support: command deregistration, alias removal,
// file deletion, and Python sys.modules purge.
// Mirrors the logic from Python ModuleUnloaderMixin.
package loader

import (
	"fmt"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// KernelForUnload – interface the kernel must satisfy for all unload helpers.
// ---------------------------------------------------------------------------

// KernelForUnload is the minimal interface that the unload helpers require
// from the kernel.  *kernel.Kernel satisfies this interface.
type KernelForUnload interface {
	// Loader management.
	GetLoader() *Loader

	// Command management.
	UnregisterCommand(name string)
	CommandsOwnedBy(moduleName string) []string

	// Alias management.
	RemoveAlias(alias string)
	AliasesForModule(moduleName string) []string

	// Paths.
	GetModulesDir() string
	GetModulesLoadedDir() string
}

// ---------------------------------------------------------------------------
// UnloadModule – original simple unload (kept for backwards compat).
// ---------------------------------------------------------------------------

// UnloadModule removes the module named name from the kernel's loader.
//
// It calls m.OnUnload(k) before removing the registry entry so that the
// module can clean up commands, cancel loops, etc.
//
// If force is true the module is removed from the registry even when OnUnload
// returns an error; the OnUnload error is still returned to the caller.
func UnloadModule(k KernelForUnload, name string, force bool) error {
	l := k.GetLoader()
	if l == nil {
		return fmt.Errorf("UnloadModule: no Loader available on kernel")
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	m, ok := l.registry.Get(name)
	if !ok {
		if force {
			return nil // idempotent when forced
		}
		return fmt.Errorf("module %q is not loaded", name)
	}

	// Run the module's teardown hook.
	unloadErr := m.OnUnload(k)
	if unloadErr != nil && !force {
		return fmt.Errorf("OnUnload %q: %w", name, unloadErr)
	}

	// Remove the registry entry regardless of OnUnload result when forced.
	if regErr := l.registry.Unregister(name); regErr != nil && !force {
		return regErr
	}

	return unloadErr
}

// ---------------------------------------------------------------------------
// UnregisterModuleCommands
// ---------------------------------------------------------------------------

// UnregisterModuleCommands removes all command handlers and their docs that
// are owned by moduleName.
//
// If force is false and the module has no commands, the call is a no-op.
func UnregisterModuleCommands(k KernelForUnload, moduleName string, force bool) error {
	cmds := k.CommandsOwnedBy(moduleName)
	if len(cmds) == 0 && !force {
		return nil
	}
	for _, cmd := range cmds {
		k.UnregisterCommand(cmd)
	}
	return nil
}

// ---------------------------------------------------------------------------
// GetModulePath / RemoveModuleFile
// ---------------------------------------------------------------------------

// GetModulePath returns the filesystem path for a module's .py file.
//
// It checks modulesLoadedDir first; if the file is not there it falls back to
// modulesDir.
func GetModulePath(modulesDir, modulesLoadedDir, name string) string {
	loaded := filepath.Join(modulesLoadedDir, name+".py")
	if _, err := os.Stat(loaded); err == nil {
		return loaded
	}
	return filepath.Join(modulesDir, name+".py")
}

// RemoveModuleFile deletes the .py file for the named module.
//
// It attempts both modulesLoadedDir and modulesDir and removes the first one
// it finds.  Returns an error only when the file exists but cannot be removed.
func RemoveModuleFile(modulesDir, modulesLoadedDir, name string) error {
	path := GetModulePath(modulesDir, modulesLoadedDir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil // already gone – not an error
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("RemoveModuleFile %q: %w", path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// RemoveModuleAliases
// ---------------------------------------------------------------------------

// RemoveModuleAliases removes all aliases that point to commands owned by
// moduleName.  The optional cmds slice can include already-removed command
// names (for which owner information is no longer available) so that their
// aliases are also cleaned up.
func RemoveModuleAliases(k KernelForUnload, moduleName string, cmds []string) {
	// Aliases derived from current owners.
	for _, alias := range k.AliasesForModule(moduleName) {
		k.RemoveAlias(alias)
	}
}

// ---------------------------------------------------------------------------
// PurgeModuleFromSysModules
// ---------------------------------------------------------------------------

// pyPurger is the interface a Python bridge must implement for
// PurgeModuleFromSysModules to call through.
type pyPurger interface {
	PurgePyModule(modName string) error
}

// PurgeModuleFromSysModules removes the module from Python's sys.modules via
// the pybridge.  bridge should be a *pybridge.Bridge (or any type that
// implements the pyPurger interface).
//
// Returns nil when bridge is nil or does not implement pyPurger (best-effort,
// non-fatal).
func PurgeModuleFromSysModules(bridge interface{}, moduleName string) error {
	if bridge == nil {
		return nil
	}
	purger, ok := bridge.(pyPurger)
	if !ok {
		return nil // bridge doesn't support purge – non-fatal
	}
	return purger.PurgePyModule(moduleName)
}

// ---------------------------------------------------------------------------
// FullUnload
// ---------------------------------------------------------------------------

// FullUnload performs a complete module unload:
//  1. Unregisters all commands via UnregisterModuleCommands.
//  2. Removes aliases via RemoveModuleAliases.
//  3. Removes the .py file when deleteFile is true.
//  4. Purges the module from Python sys.modules via the bridge.
//
// If force is true, steps 1 and 2 always proceed even when the module appears
// not to be loaded.  Errors from individual steps are collected and returned
// as a combined error.
func FullUnload(k KernelForUnload, bridge interface{}, moduleName string, deleteFile bool, force bool) error {
	var errs []error

	// Step 1 – unregister commands.
	if err := UnregisterModuleCommands(k, moduleName, force); err != nil {
		errs = append(errs, fmt.Errorf("unregister commands: %w", err))
	}

	// Step 2 – remove aliases.
	RemoveModuleAliases(k, moduleName, nil)

	// Step 3 – remove loader registry entry (best-effort).
	if l := k.GetLoader(); l != nil {
		_ = UnloadModule(k, moduleName, force)
	}

	// Step 4 – delete .py file.
	if deleteFile {
		if err := RemoveModuleFile(k.GetModulesDir(), k.GetModulesLoadedDir(), moduleName); err != nil {
			errs = append(errs, fmt.Errorf("remove file: %w", err))
		}
	}

	// Step 5 – purge from Python sys.modules.
	if err := PurgeModuleFromSysModules(bridge, moduleName); err != nil {
		errs = append(errs, fmt.Errorf("purge sys.modules: %w", err))
	}

	if len(errs) == 0 {
		return nil
	}
	// Combine errors.
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("FullUnload %q: %s", moduleName, joinStrings(msgs, "; "))
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}
