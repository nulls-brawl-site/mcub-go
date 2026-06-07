package kernel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/database"
	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/session"
)

// Init performs all pre-run initialisation:
//   1. Opens the SQLite database.
//   2. Creates the Telegram client.
//   3. Registers event handlers.
func (k *Kernel) Init() error {
	k.Log.Info("Initialising MCUB kernel (%s) v%s", k.Type, k.Version)

	// Open database.
	dbPath := "mcub.db"
	dbVer := 2
	if k.Config != nil {
		dbVer = k.Config.DBVersion
	}
	db, err := database.Open(dbPath, dbVer)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	k.DB = db
	k.Log.Info("Database opened at %s (version %d)", dbPath, dbVer)

	// Load Python modules (system and user directories).
	if err := k.LoadPyModules(k.ModulesDir); err != nil {
		k.Log.Warn("LoadPyModules(%s): %v", k.ModulesDir, err)
	}
	if err := k.LoadPyModules(k.ModulesLoadedDir); err != nil {
		k.Log.Warn("LoadPyModules(%s): %v", k.ModulesLoadedDir, err)
	}

	// Create Telegram client.
	if k.Config == nil {
		return fmt.Errorf("config not set")
	}

	sessionStorage, err := session.NewFileSessionStorage("mcub.session")
	if err != nil {
		return fmt.Errorf("create session storage: %w", err)
	}

	cli, err := mcubclient.New(mcubclient.Options{
		AppID:          int(k.Config.APIID),
		AppHash:        k.Config.APIHash,
		Session:        sessionStorage.Storage(),
		ProtectionMode: 0, // default = safe
		Logger:         k.Log,
	})
	if err != nil {
		return fmt.Errorf("create telegram client: %w", err)
	}
	k.Client = cli
	k.Log.Info("Telegram client created (APIID=%d)", k.Config.APIID)

	// Register event handlers.
	k.RegisterHandlers()
	k.Log.Info("Event handlers registered")

	return nil
}

// Run connects to Telegram and blocks until the context is cancelled or
// ShutdownFlag is set.
func (k *Kernel) Run(ctx context.Context) error {
	if k.Client == nil {
		return fmt.Errorf("kernel not initialised – call Init() first")
	}

	k.Log.Info("Connecting to Telegram...")

	var runErr error
	done := make(chan struct{})

	go func() {
		defer close(done)
		runErr = k.Client.Run(ctx, func(ctx context.Context) error {
			k.Log.Info("Connected to Telegram (uptime timer started)")
			k.StartTime = time.Now()

			// Optionally log self.
			if self, err := k.Client.Self(ctx); err == nil {
				k.Log.Info("Authenticated as user ID %d", self.ID)
				k.AdminID = int64(self.ID)
			}

			// Block until context is done or shutdown is requested.
			ticker := time.NewTicker(time.Duration(k.healthcheckInterval()) * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					k.Log.Debug("Healthcheck ping (uptime %s)", k.Uptime().Round(time.Second))
					k.mu.RLock()
					sd := k.ShutdownFlag
					k.mu.RUnlock()
					if sd {
						k.Log.Info("Shutdown flag detected – disconnecting")
						return nil
					}
				}
			}
		})
	}()

	<-done
	return runErr
}

// Shutdown signals the kernel to stop on the next healthcheck tick.
func (k *Kernel) Shutdown() {
	k.mu.Lock()
	k.ShutdownFlag = true
	k.mu.Unlock()
	k.Log.Info("Shutdown requested")
}

// Restart saves the config, shuts down, and re-execs the current process.
func (k *Kernel) Restart() error {
	k.Log.Info("Restart requested")

	// Save config before restarting.
	if k.Config != nil && k.ConfigFile != "" {
		if err := k.Config.Save(k.ConfigFile); err != nil {
			k.Log.Warn("Failed to save config before restart: %v", err)
		}
	}

	// Close database.
	if k.DB != nil {
		if err := k.DB.Close(); err != nil {
			k.Log.Warn("Failed to close DB before restart: %v", err)
		}
	}

	// Re-exec self.
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	k.Log.Info("Re-executing %s", exe)
	return reExec(exe, os.Args[1:])
}

// healthcheckInterval returns the configured interval, defaulting to 30 s.
func (k *Kernel) healthcheckInterval() int {
	if k.Config != nil && k.Config.HealthcheckInterval > 0 {
		return k.Config.HealthcheckInterval
	}
	return 30
}

// LoadPyModules scans dir for .py files and loads them through the Python bridge.
// Missing directories are silently skipped.
func (k *Kernel) LoadPyModules(dir string) error {
	k.Log.Info("Loading Python modules from %s", dir)

	bridge, err := pybridge.NewBridge()
	if err != nil {
		return fmt.Errorf("init Python bridge: %w", err)
	}

	// Wire kernel-wide callbacks so Python can call back into Go.
	pybridge.SetKernelCallbacks(pybridge.KernelCallbacks{
		GetPrefix:    k.getPrefix,
		GetVersion:   k.getVersion,
		GetStartTime: k.getStartTimestamp,
		LogInfo:      func(msg string) { k.Log.Info("[py] %s", msg) },
		LogDebug:     func(msg string) { k.Log.Debug("[py] %s", msg) },
		LogWarn:      func(msg string) { k.Log.Warn("[py] %s", msg) },
		LogError:     func(msg string) { k.Log.Error("[py] %s", msg) },
		DBGet: func(module, key string) string {
			if k.DB == nil {
				return ""
			}
			v, _, _ := k.DB.Get(module + ":" + key)
			return v
		},
		DBSet: func(module, key, value string) {
			if k.DB != nil {
				_ = k.DB.Set(module+":"+key, value)
			}
		},
		DBDelete: func(module, key string) {
			if k.DB != nil {
				_ = k.DB.Delete(module + ":" + key)
			}
		},
		RestartKernel: func() {
			go func() {
				if err := k.Restart(); err != nil {
					k.Log.Error("Restart triggered from Python failed: %v", err)
				}
			}()
		},
	})

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			k.Log.Info("Python modules dir %s does not exist, skipping", dir)
			return nil
		}
		return err
	}

	loaded := 0
	failed := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".py") {
			continue
		}
		if name == "__init__.py" {
			continue
		}

		path := filepath.Join(dir, name)
		modName := strings.TrimSuffix(name, ".py")

		k.Log.Debug("Loading Python module %s from %s", modName, path)

		pyMod, err := bridge.LoadPyModule(path)
		if err != nil {
			k.Log.Error("Failed to load Python module %s: %v", modName, err)
			failed++
			continue
		}

		pm := pybridge.NewPythonModule(bridge, pyMod)
		if err := k.Loader.LoadBuiltin(pm); err != nil {
			k.Log.Error("Failed to register Python module %s: %v", modName, err)
			failed++
			continue
		}

		k.mu.Lock()
		k.SystemModules[pm.Name()] = pm
		k.mu.Unlock()

		k.Log.Info("Loaded Python module %s (commands: %d)", pm.Name(), len(pm.Commands()))
		loaded++
	}

	k.Log.Info("Python modules loaded: %d OK, %d failed", loaded, failed)
	return nil
}
