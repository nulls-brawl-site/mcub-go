package loader

import (
	"fmt"
	"plugin"
	"sync"
)

// Loader manages the lifecycle of MCUB modules.
// Built-in modules are registered directly; Go plugin (.so) modules are loaded
// from the filesystem via the plugin package.
type Loader struct {
	mu       sync.Mutex
	registry *Registry
	kernel   interface{}
	plugins  map[string]*plugin.Plugin // path -> loaded plugin
}

// NewLoader creates a Loader that will call OnLoad/OnUnload with the given
// kernel instance.
func NewLoader(kernel interface{}) *Loader {
	return &Loader{
		registry: NewRegistry(),
		kernel:   kernel,
		plugins:  make(map[string]*plugin.Plugin),
	}
}

// Registry returns the underlying module registry.
func (l *Loader) Registry() *Registry {
	return l.registry
}

// LoadBuiltin registers and initialises a built-in module.
func (l *Loader) LoadBuiltin(m Module) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.registry.Register(m, ModuleSourceBuiltin); err != nil {
		return err
	}
	if err := m.OnLoad(l.kernel); err != nil {
		_ = l.registry.Unregister(m.Name())
		return fmt.Errorf("OnLoad %q: %w", m.Name(), err)
	}
	return nil
}

// LoadPlugin loads a Go plugin (.so file) from path.
// The plugin must export a symbol "Module" of type loader.Module.
func (l *Loader) LoadPlugin(path string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, loaded := l.plugins[path]; loaded {
		return fmt.Errorf("plugin %q already loaded", path)
	}

	p, err := plugin.Open(path)
	if err != nil {
		return fmt.Errorf("open plugin %q: %w", path, err)
	}

	sym, err := p.Lookup("Module")
	if err != nil {
		return fmt.Errorf("plugin %q has no Module symbol: %w", path, err)
	}

	m, ok := sym.(Module)
	if !ok {
		// Also accept *Module pointer exported by the plugin.
		mp, ok2 := sym.(*Module)
		if !ok2 || mp == nil {
			return fmt.Errorf("plugin %q Module symbol has unexpected type", path)
		}
		m = *mp
	}

	if err := l.registry.Register(m, ModuleSourcePlugin); err != nil {
		return err
	}
	if err := m.OnLoad(l.kernel); err != nil {
		_ = l.registry.Unregister(m.Name())
		return fmt.Errorf("OnLoad plugin %q: %w", path, err)
	}
	l.plugins[path] = p
	return nil
}

// Unload unregisters and tears down a module by name.
func (l *Loader) Unload(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	m, ok := l.registry.Get(name)
	if !ok {
		return fmt.Errorf("module %q not loaded", name)
	}
	if err := m.OnUnload(l.kernel); err != nil {
		return fmt.Errorf("OnUnload %q: %w", name, err)
	}
	return l.registry.Unregister(name)
}

// AllCommands flattens all commands from all loaded modules into a single slice.
func (l *Loader) AllCommands() []Command {
	all := l.registry.All()
	var cmds []Command
	for _, m := range all {
		cmds = append(cmds, m.Commands()...)
	}
	return cmds
}
