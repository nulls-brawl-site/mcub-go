// Package kernel is the heart of the MCUB userbot. It wires together the
// config, database, client, and module loader into a single runtime object.
package kernel

import (
	"sync"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/config"
	"github.com/nulls-brawl-site/mcub-go/internal/database"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/mcub-go/internal/logger"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// CommandHandler is an alias for loader.CommandHandler for ergonomic use within
// the kernel package.
type CommandHandler = loader.CommandHandler

// CommandDoc stores documentation for a registered command.
type CommandDoc struct {
	Description string
	ModuleName  string
	Usage       string
}

// Module is an alias for loader.Module.
type Module = loader.Module

// ModuleSource is an alias for loader.ModuleSource.
type ModuleSource = loader.ModuleSource

// Middleware is a function that wraps an event handler.
type Middleware func(next events.Handler) events.Handler

// KernelType identifies which kernel variant is running.
type KernelType string

const (
	KernelStandard KernelType = "standard"
	KernelZen      KernelType = "zen"
)

// Kernel is the central runtime object of the MCUB userbot.
// It holds all registries, subsystem references and runtime state.
type Kernel struct {
	mu sync.RWMutex

	// --- Identity ---
	Version   string
	StartTime time.Time
	Type      KernelType

	// --- Config ---
	Config     *config.Config
	ConfigFile string

	// --- Effective runtime values (may differ from config after CLI overrides) ---
	CustomPrefix string
	AdminID      int64
	APIID        int64
	APIHash      string

	// --- Command registries ---
	CommandHandlers map[string]CommandHandler
	CommandOwners   map[string]string // command name -> module name
	CommandDocs     map[string]CommandDoc

	// --- Module registries ---
	LoadedModules map[string]Module
	SystemModules map[string]Module

	// --- Aliases ---
	Aliases map[string]string

	// --- Module source tracking ---
	ModuleSources map[string]ModuleSource

	// --- Paths ---
	ModulesDir       string
	ModulesLoadedDir string

	// --- Runtime flags ---
	ShutdownFlag bool

	// --- Subsystems ---
	DB     *database.Database
	Log    *logger.Logger
	Loader *loader.Loader
	Client *mcubclient.MCUBClient

	// --- Middleware ---
	middlewares []Middleware
}

// New creates a new Kernel with sane defaults.
func New(cfg *config.Config, configFile string, kType KernelType) *Kernel {
	log := logger.Default()
	k := &Kernel{
		Version:          "1.0.0",
		StartTime:        time.Now(),
		Type:             kType,
		Config:           cfg,
		ConfigFile:       configFile,
		CustomPrefix:     cfg.CommandPrefix,
		APIID:            cfg.APIID,
		APIHash:          cfg.APIHash,
		CommandHandlers:  make(map[string]CommandHandler),
		CommandOwners:    make(map[string]string),
		CommandDocs:      make(map[string]CommandDoc),
		LoadedModules:    make(map[string]Module),
		SystemModules:    make(map[string]Module),
		Aliases:          cfg.Aliases,
		ModuleSources:    make(map[string]ModuleSource),
		ModulesDir:       "modules",
		ModulesLoadedDir: "modules_loaded",
		Log:              log,
	}
	k.Loader = loader.NewLoader(k)
	return k
}

// Prefix returns the active command prefix.
func (k *Kernel) Prefix() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.CustomPrefix
}

// SetPrefix updates the active command prefix.
func (k *Kernel) SetPrefix(p string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.CustomPrefix = p
}

// RegisterCommand adds a command handler to the kernel's registry.
// If the command already exists it is silently replaced.
func (k *Kernel) RegisterCommand(name, moduleName, description string, h CommandHandler) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.CommandHandlers[name] = h
	k.CommandOwners[name] = moduleName
	k.CommandDocs[name] = CommandDoc{
		Description: description,
		ModuleName:  moduleName,
	}
}

// UnregisterCommand removes a command from the registry.
func (k *Kernel) UnregisterCommand(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.CommandHandlers, name)
	delete(k.CommandOwners, name)
	delete(k.CommandDocs, name)
}

// AddMiddleware appends a kernel-level event middleware.
func (k *Kernel) AddMiddleware(mw Middleware) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.middlewares = append(k.middlewares, mw)
}

// Middlewares returns a copy of the current middleware slice.
func (k *Kernel) Middlewares() []Middleware {
	k.mu.RLock()
	defer k.mu.RUnlock()
	cp := make([]Middleware, len(k.middlewares))
	copy(cp, k.middlewares)
	return cp
}

// Uptime returns how long the kernel has been running.
func (k *Kernel) Uptime() time.Duration {
	return time.Since(k.StartTime)
}

// GetClient returns the MCUBClient (used by pybridge via duck-typed interface).
func (k *Kernel) GetClient() *mcubclient.MCUBClient {
	return k.Client
}

// getClient implements kernelIface (unexported, called by pybridge).
func (k *Kernel) getClient() *mcubclient.MCUBClient {
	return k.Client
}

// getPrefix implements kernelIface.
func (k *Kernel) getPrefix() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.CustomPrefix
}

// getVersion implements kernelIface.
func (k *Kernel) getVersion() string {
	return k.Version
}

// getStartTimestamp implements kernelIface.
func (k *Kernel) getStartTimestamp() int64 {
	return k.StartTime.Unix()
}

// registerCommand implements kernelIface (lowercase, delegates to exported form).
func (k *Kernel) registerCommand(name, moduleName, description string, h loader.CommandHandler) {
	k.RegisterCommand(name, moduleName, description, h)
}

// unregisterCommand implements kernelIface (lowercase, delegates to exported form).
func (k *Kernel) unregisterCommand(name string) {
	k.UnregisterCommand(name)
}
