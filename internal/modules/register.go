package modules

import "github.com/nulls-brawl-site/mcub-go/internal/loader"

// AllSystemModules returns all built-in system modules. These are loaded into
// the kernel's SystemModules registry at startup and cannot be unloaded.
func AllSystemModules() []loader.Module {
	return []loader.Module{
		newCoreModule(),
		newModulesModule(),
	}
}
