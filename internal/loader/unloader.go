// Package loader – unloader.go
//
// UnloadModule is a convenience function that removes a module from the
// loader's registry, calling its OnUnload hook first. It mirrors the
// unload_module logic from the Python DependencyManagerMixin / kernel.
package loader

import "fmt"

// KernelForUnload is the minimal interface that UnloadModule requires from the
// kernel. The *kernel.Kernel type satisfies this interface.
type KernelForUnload interface {
	// GetLoader returns the Loader that manages module lifecycle.
	GetLoader() *Loader
}

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

	// Return the OnUnload error so callers can log it even in force mode.
	return unloadErr
}
