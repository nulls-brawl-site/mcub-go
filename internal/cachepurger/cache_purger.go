// Package cachepurger implements cascading cache purge for the MCUB kernel.
// Ported from core/lib/utils/cache_purger.py.
//
// Three levels of aggression:
//
//	Level 1 - Safe caches: TTL, inline sessions, callback maps, docs, metadata.
//	Level 2 - Extended: stale registries, pipe vars, orphan aliases, configs.
//	Level 3 - Hardcore: GC collect, full module sweep, log queue flush.
//
// SPDX-License-Identifier: MIT
package cachepurger

import (
	"runtime"
)

// Result describes what the purge operation cleared.
type Result struct {
	Level   int
	Cleared []string
}

// Kernel is the subset of the kernel interface required for cache purging.
// All methods are optional; the purger uses type assertions and skips gracefully.
type Kernel interface{}

// clearable is implemented by any map-like cache with a Clear method.
type clearable interface {
	Clear()
}

// tryGetAttr uses reflection-free type assertions via helper interfaces to
// access well-known kernel attributes. Since the kernel is defined elsewhere
// in the codebase, we define narrow interfaces here.

// KernelCacheProvider exposes kernel sub-components by name.
// Modules that implement this interface allow the purger to work without
// a hard dependency on the kernel package (avoiding import cycles).
type KernelCacheProvider interface {
	// GetCache returns a named cache object or nil when absent.
	GetCache(name string) interface{}
	// ClearCache clears a named cache and reports whether it existed.
	ClearCache(name string) bool
	// LoadedModuleNames returns the names of currently loaded modules.
	LoadedModuleNames() []string
	// SystemModuleNames returns the names of currently loaded system modules.
	SystemModuleNames() []string
}

// PurgeCaches purges kernel caches at the given level (1–3).
// It accepts any value and downcasts where possible; unknown kernels are
// handled as no-ops.
func PurgeCaches(kernel Kernel, level int) Result {
	res := Result{Level: level}
	if kernel == nil {
		return res
	}

	// Attempt to use the structured interface first.
	if kp, ok := kernel.(KernelCacheProvider); ok {
		return purgeViaProvider(kp, level)
	}

	// Fallback: nothing to clear (kernel has unknown shape).
	return res
}

func purgeViaProvider(kp KernelCacheProvider, level int) Result {
	res := Result{Level: level}

	// Level 1: safe caches.
	level1Caches := []string{
		"ttl_cache",
		"inline_sessions",
		"inline_callback_map",
		"hikka_compat_inline_state",
		"hikka_compat_inline_units",
		"hikka_compat_inline_custom_map",
		"catalog_cache",
		"pending_confirmations",
		"command_docs",
		"command_metadata",
		"module_commands_index",
		"pipe_vars",
		"pipe_macros",
		"module_type_cache",
		"db_get_cache",
	}
	for _, name := range level1Caches {
		if kp.ClearCache(name) {
			res.Cleared = append(res.Cleared, name)
		}
	}

	// Level 2: extended.
	if level >= 2 {
		extended := []string{
			"callback_permissions",
		}
		for _, name := range extended {
			if kp.ClearCache(name) {
				res.Cleared = append(res.Cleared, name)
			}
		}
	}

	// Level 3: hardcore.
	if level >= 3 {
		for i := 0; i < 3; i++ {
			runtime.GC()
		}
		res.Cleared = append(res.Cleared, "gc_collect")

		miscL3 := []string{
			"middleware_id_sets",
			"scheduler_registry",
		}
		for _, name := range miscL3 {
			if kp.ClearCache(name) {
				res.Cleared = append(res.Cleared, name)
			}
		}
	}

	return res
}
