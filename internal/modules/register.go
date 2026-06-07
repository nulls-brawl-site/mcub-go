package modules

import "github.com/nulls-brawl-site/mcub-go/internal/loader"

// AllSystemModules returns all built-in system modules. These are loaded into
// the kernel's SystemModules registry at startup and cannot be unloaded.
func AllSystemModules() []loader.Module {
	return []loader.Module{
		newCoreModule(),      // ping (Go built-in), restart, info
		newModulesModule(),   // man, iload, um, reload
		newTesterModule(),    // ping (tester), logs, freezing, teaser
		newUpdatesModule(),   // restart (updates), update, stop
		newInfoModule(),      // info (MCUB_info)
	}
}
