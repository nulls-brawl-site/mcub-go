// Package kernel is the heart of the MCUB userbot. It wires together the
// config, database, client, and module loader into a single runtime object.
package kernel

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/cache"
	"github.com/nulls-brawl-site/mcub-go/internal/config"
	"github.com/nulls-brawl-site/mcub-go/internal/database"
	"github.com/nulls-brawl-site/mcub-go/internal/inline"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/mcub-go/internal/logger"
	"github.com/nulls-brawl-site/mcub-go/internal/permissions"
	"github.com/nulls-brawl-site/mcub-go/internal/scheduler"
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
	KernelMini     KernelType = "mini"
	KernelBot      KernelType = "bot"
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

	// --- Python bridge (set during Init, stored as interface{} so kernel.go
	//     does not need to import pybridge directly) ---
	PyBridge interface{}

	// --- Runtime flags ---
	ShutdownFlag bool

	// --- Subsystems ---
	DB          *database.Database
	Log         *logger.Logger
	Loader      *loader.Loader
	Client      *mcubclient.MCUBClient
	Cache       *cache.TTLCache
	Scheduler   *scheduler.TaskScheduler
	Permissions *permissions.CallbackPermissionManager

	// --- Middleware ---
	middlewares []Middleware

	// --- Generic middleware chains (ported from kernel_handlers.py) ---
	middlewareChain        []MiddlewareFunc
	requestMiddlewareChain []RequestMiddlewareFunc

	// --- Inline / callback handler registries ---
	// key -> handler (opaque); populated by RegisterInlineHandler /
	// RegisterCallbackHandler.
	inlineHandlers      map[string]interface{}
	inlineHandlerOwners map[string]string // key -> module name
	callbackHandlers    map[string]interface{}

	// --- Inline subsystem ---
	// InlineManager coordinates the inline bot and its handler registry.
	InlineManager *inline.Manager

	// --- Repository manager ---
	RepoManager *loader.RepositoryManager

	// --- Loading phase: "system", "user", "full" ---
	loadPhase string

	// --- Bot-specific fields (KernelBot only) ---
	BotToken  string
	BotID     int64
	IsBotMode bool
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
		ModuleSources:          make(map[string]ModuleSource),
		ModulesDir:             "modules",
		ModulesLoadedDir:       "modules_loaded",
		Log:                    log,
		inlineHandlers:         make(map[string]interface{}),
		inlineHandlerOwners:    make(map[string]string),
		callbackHandlers:       make(map[string]interface{}),
		middlewareChain:        nil,
		requestMiddlewareChain: nil,
	}
	k.Loader = loader.NewLoader(k)
	k.Cache = cache.New(500, 10*time.Minute)
	k.Scheduler = scheduler.New(log)
	k.Permissions = permissions.New()
	k.InlineManager = inline.NewManager(k)
	k.RepoManager = loader.NewRepositoryManager()
	return k
}

// NewMiniKernel creates a lightweight Mini kernel (port of mini.py).
// It skips the web panel and loads only essential system modules.
func NewMiniKernel(cfg *config.Config, configFile string) *Kernel {
	k := New(cfg, configFile, KernelMini)
	k.loadPhase = "mini"
	return k
}

// NewBotKernel creates a Bot kernel (port of bot.py).
// It uses a bot token for authentication instead of a user phone number.
func NewBotKernel(cfg *config.Config, configFile, token string) *Kernel {
	k := New(cfg, configFile, KernelBot)
	k.BotToken = token
	k.IsBotMode = true
	return k
}

// GetKernelTag returns a short display string for the kernel type.
// Matches the Python _kernel_tag attribute values.
func (k *Kernel) GetKernelTag() string {
	switch k.Type {
	case KernelBot:
		return "BOT"
	case KernelMini:
		return "MINI"
	case KernelZen:
		return "zen"
	default:
		return "standard"
	}
}

// HealthcheckIntervalSec returns the healthcheck interval in seconds.
// Exported wrapper around the unexported healthcheckInterval for tests.
func (k *Kernel) HealthcheckIntervalSec() int {
	return k.healthcheckInterval()
}

// GetAPIID returns the Telegram app ID configured for this kernel.
func (k *Kernel) GetAPIID() int64 {
	return k.APIID
}

// GetAPIHash returns the Telegram app hash configured for this kernel.
func (k *Kernel) GetAPIHash() string {
	return k.APIHash
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

// --- Module-map helpers (used by loader.SystemLoader / loader.UserLoader) ---

// StoreSystemModule stores a loaded system module in the SystemModules map.
// Satisfies loader.systemKernelStore.
func (k *Kernel) StoreSystemModule(name string, m loader.Module) {
	k.mu.Lock()
	k.SystemModules[name] = m
	k.mu.Unlock()
}

// StoreUserModule stores a loaded user module in the LoadedModules map.
// Satisfies loader.userKernelStore.
func (k *Kernel) StoreUserModule(name string, m loader.Module) {
	k.mu.Lock()
	k.LoadedModules[name] = m
	k.mu.Unlock()
}

// --- kernelDepsIface helpers (used by loader.LoadUserModules package func) ---

// GetLoader returns the kernel's module Loader.
func (k *Kernel) GetLoader() *loader.Loader {
	return k.Loader
}

// GetLogIface returns the kernel's logger as a loader.LogIface.
func (k *Kernel) GetLogIface() loader.LogIface {
	return k.Log
}

// GetBridge returns the Python bridge stored during Init.
func (k *Kernel) GetBridge() interface{} {
	return k.PyBridge
}

// CommandsOwnedBy returns a snapshot of command names owned by moduleName.
func (k *Kernel) CommandsOwnedBy(moduleName string) []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	var cmds []string
	for cmd, owner := range k.CommandOwners {
		if owner == moduleName {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// AliasesForModule returns all alias names that point to commands owned by
// moduleName.
func (k *Kernel) AliasesForModule(moduleName string) []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	var aliases []string
	for alias, target := range k.Aliases {
		if owner, ok := k.CommandOwners[target]; ok && owner == moduleName {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

// GetModulesDir returns the path to the modules directory.
func (k *Kernel) GetModulesDir() string {
	return k.ModulesDir
}

// GetModulesLoadedDir returns the path to the loaded-modules directory.
func (k *Kernel) GetModulesLoadedDir() string {
	return k.ModulesLoadedDir
}

// ─────────────────────────────────────────────────────────────────────────────
// KernelAPI helpers (web panel + admin queries)
// ─────────────────────────────────────────────────────────────────────────────

// GetAliases returns a snapshot of the current command aliases map.
func (k *Kernel) GetAliases() map[string]string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.Config == nil {
		return nil
	}
	out := make(map[string]string, len(k.Config.Aliases))
	for a, t := range k.Config.Aliases {
		out[a] = t
	}
	return out
}

// GetLanguage returns the configured language, defaulting to "en".
func (k *Kernel) GetLanguage() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.Config != nil && k.Config.Language != "" {
		return k.Config.Language
	}
	return "en"
}

// GetAdminID returns the configured admin user ID.
func (k *Kernel) GetAdminID() int64 {
	return k.AdminID
}

// GetRepos returns the list of repository URLs managed by the RepoManager.
func (k *Kernel) GetRepos() []string {
	if k.RepoManager == nil {
		return nil
	}
	repos := k.RepoManager.ListRepos()
	urls := make([]string, len(repos))
	for i, r := range repos {
		urls[i] = r.URL
	}
	return urls
}

// AddRepo adds a repository URL via the RepoManager (performs SSRF validation
// and verifies the repo is reachable).
func (k *Kernel) AddRepo(url string) error {
	if k.RepoManager == nil {
		k.RepoManager = loader.NewRepositoryManager()
	}
	return k.RepoManager.AddRepoURL(url)
}

// RemoveRepo removes the repository at the given 0-based index.
func (k *Kernel) RemoveRepo(index int) error {
	if k.RepoManager == nil {
		return fmt.Errorf("no repository manager")
	}
	return k.RepoManager.RemoveRepo(index)
}

// GetConfig returns a masked copy of the current config (secrets replaced with
// "***"). The returned value can be safely serialised to JSON.
func (k *Kernel) GetConfig() interface{} {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.Config == nil {
		return nil
	}
	m := k.Config.ToMap()
	// Mask sensitive fields.
	for _, field := range []string{"api_hash", "phone", "proxy", "inline_bot_token", "web_panel_token"} {
		if v, ok := m[field]; ok && v != nil && v != "" {
			m[field] = "***"
		}
	}
	return m
}

// UpdateConfig merges updates into the current config and saves it to disk.
func (k *Kernel) UpdateConfig(updates map[string]interface{}) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Config == nil {
		return fmt.Errorf("config not initialised")
	}
	k.Config.Merge(updates)
	if k.ConfigFile != "" {
		return k.Config.Save(k.ConfigFile)
	}
	return nil
}

// ShouldProcessCommandEvent returns true when the event should be dispatched
// as a command (outgoing messages always qualify; incoming only from admin).
func (k *Kernel) ShouldProcessCommandEvent(senderID int64, isOutgoing bool) bool {
	if isOutgoing {
		return true
	}
	return k.AdminID != 0 && senderID == k.AdminID
}

// IsAdmin returns true when userID equals the configured admin ID.
func (k *Kernel) IsAdmin(userID int64) bool {
	return k.AdminID != 0 && userID == k.AdminID
}

// IsBotAvailable returns true when the inline bot client is ready.
func (k *Kernel) IsBotAvailable() bool {
	return k.InlineManager != nil && k.InlineManager.IsRunning()
}

// GetModuleMetadata parses module metadata comments from Python source code.
// It looks for lines like `# version: X.Y.Z`, `# author: @name`,
// `# description: …` as well as a `class …Module` name attribute.
func (k *Kernel) GetModuleMetadata(code string) map[string]interface{} {
	result := map[string]interface{}{
		"version":     "1.0.0",
		"author":      "unknown",
		"description": "",
		"commands":    map[string]string{},
	}
	for _, line := range strings.Split(code, "\n") {
		stripped := strings.TrimSpace(line)
		if !strings.HasPrefix(stripped, "#") {
			continue
		}
		content := strings.TrimSpace(strings.TrimPrefix(stripped, "#"))
		if strings.HasPrefix(content, "version:") {
			result["version"] = strings.TrimSpace(strings.TrimPrefix(content, "version:"))
		} else if strings.HasPrefix(content, "author:") {
			result["author"] = strings.TrimSpace(strings.TrimPrefix(content, "author:"))
		} else if strings.HasPrefix(content, "description:") {
			result["description"] = strings.TrimSpace(strings.TrimPrefix(content, "description:"))
		}
	}
	return result
}

// SetPowerSaveMode enables or disables power-save mode in the config.
func (k *Kernel) SetPowerSaveMode(enabled bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Config != nil {
		k.Config.PowerSaveMode = enabled
	}
}

// GetLoadKernelStatus returns the current loading phase: "system", "user", or "full".
func (k *Kernel) GetLoadKernelStatus() string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if k.loadPhase == "" {
		return "full"
	}
	return k.loadPhase
}

// SetLoadPhase sets the kernel loading phase (used during Init).
func (k *Kernel) SetLoadPhase(phase string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.loadPhase = phase
}
