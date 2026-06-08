package loader

import (
	"fmt"
	"plugin"
	"sync"

	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
)

// Loader manages the lifecycle of MCUB modules.
// Built-in modules are registered directly; Go plugin (.so) modules are loaded
// from the filesystem via the plugin package; Python modules are loaded via
// the embedded Python bridge through PyLoader.
type Loader struct {
	mu       sync.Mutex
	registry *Registry
	kernel   interface{}
	plugins  map[string]*plugin.Plugin // path -> loaded plugin
	pyLoader *PyLoader                 // optional Python module loader
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
	// Register in kernel's SystemModules map so .man/.ping show correct count.
	type systemStorer interface {
		StoreSystemModule(name string, m Module)
	}
	if ss, ok := l.kernel.(systemStorer); ok {
		ss.StoreSystemModule(m.Name(), m)
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

// SetPyLoader configures the Python loader used by LoadPyFile and
// LoadPyFromURL. It replaces any previously set PyLoader.
func (l *Loader) SetPyLoader(pl *PyLoader) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pyLoader = pl
}

// PyLoaderInstance returns the currently configured PyLoader, or nil.
func (l *Loader) PyLoaderInstance() *PyLoader {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pyLoader
}

// LoadPyFile loads the .py file at path through the Python bridge, wraps it in
// a PythonModule, and registers it with the kernel via LoadBuiltin.
//
// SetPyLoader must be called before invoking LoadPyFile.
func (l *Loader) LoadPyFile(path string) error {
	l.mu.Lock()
	pl := l.pyLoader
	l.mu.Unlock()

	if pl == nil {
		return fmt.Errorf("LoadPyFile: no PyLoader configured – call SetPyLoader first")
	}

	pyMod, err := pl.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("LoadPyFile %q: %w", path, err)
	}

	pm := NewPythonModule(pl.bridge, pyMod)
	return l.LoadBuiltin(pm)
}

// LoadPyFromURL downloads a .py module from url, saves it under destDir, and
// loads it through the Python bridge exactly like LoadPyFile.
//
// SetPyLoader must be called before invoking LoadPyFromURL.
func (l *Loader) LoadPyFromURL(url, destDir string) error {
	l.mu.Lock()
	pl := l.pyLoader
	l.mu.Unlock()

	if pl == nil {
		return fmt.Errorf("LoadPyFromURL: no PyLoader configured – call SetPyLoader first")
	}

	pyMod, err := pl.LoadFromURL(url, destDir)
	if err != nil {
		return fmt.Errorf("LoadPyFromURL %q: %w", url, err)
	}

	pm := NewPythonModule(pl.bridge, pyMod)
	return l.LoadBuiltin(pm)
}

// NewPyLoaderFromBridge is a convenience constructor that creates a PyLoader
// using the Loader's kernel reference and registers it on the Loader.
// The bridge must already be initialised (pybridge.NewBridge).
func (l *Loader) NewPyLoaderFromBridge(bridge *pybridge.Bridge) *PyLoader {
	l.mu.Lock()
	k := l.kernel
	l.mu.Unlock()

	pl := NewPyLoader(bridge, k)
	l.SetPyLoader(pl)
	return pl
}
