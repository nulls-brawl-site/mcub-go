// Package loader provides the module registry and loader for MCUB.
package loader

import (
	"context"
	"fmt"
	"sync"

	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// CommandHandler is the function signature for all MCUB command handlers.
type CommandHandler func(ctx context.Context, event *events.NewMessage) error

// Command describes a single bot command exposed by a module.
type Command struct {
	// Name is the command word without the prefix (e.g. "ping").
	Name string

	// Description is a short human-readable description shown in help.
	Description string

	// Handler is the function invoked when the command is matched.
	Handler CommandHandler
}

// CommandDoc stores documentation for a registered command.
type CommandDoc struct {
	Description string
	ModuleName  string
}

// ModuleSource indicates where a module was loaded from.
type ModuleSource string

const (
	ModuleSourceBuiltin ModuleSource = "builtin"
	ModuleSourcePlugin  ModuleSource = "plugin"
	ModuleSourceUser    ModuleSource = "user"
)

// Module is the interface all MCUB modules must implement.
type Module interface {
	// Name returns the canonical module name (used as registry key).
	Name() string

	// OnLoad is called when the module is loaded into the kernel.
	// The kernel is passed as an opaque interface to avoid import cycles.
	OnLoad(kernel interface{}) error

	// OnUnload is called when the module is unloaded.
	OnUnload(kernel interface{}) error

	// Commands returns all commands registered by this module.
	Commands() []Command
}

// Registry is a thread-safe store of modules and their commands.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]Module
	sources map[string]ModuleSource
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		modules: make(map[string]Module),
		sources: make(map[string]ModuleSource),
	}
}

// Register adds a module to the registry under its Name().
// Returns an error if a module with the same name is already registered.
func (r *Registry) Register(m Module, src ModuleSource) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := m.Name()
	if _, exists := r.modules[name]; exists {
		return fmt.Errorf("module %q already registered", name)
	}
	r.modules[name] = m
	r.sources[name] = src
	return nil
}

// Unregister removes a module by name. Returns an error if not found.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.modules[name]; !exists {
		return fmt.Errorf("module %q not found", name)
	}
	delete(r.modules, name)
	delete(r.sources, name)
	return nil
}

// Get returns the module with the given name.
func (r *Registry) Get(name string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.modules[name]
	return m, ok
}

// All returns a snapshot of all registered modules keyed by name.
func (r *Registry) All() map[string]Module {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snap := make(map[string]Module, len(r.modules))
	for k, v := range r.modules {
		snap[k] = v
	}
	return snap
}

// Source returns the ModuleSource for a named module.
func (r *Registry) Source(name string) (ModuleSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[name]
	return s, ok
}

// Names returns the sorted list of registered module names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.modules))
	for k := range r.modules {
		names = append(names, k)
	}
	return names
}
