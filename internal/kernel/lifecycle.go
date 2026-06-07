package kernel

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/database"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/session"
)

// Init performs all pre-run initialisation in the following order:
//  1. Opens the SQLite database.
//  2. Creates the Telegram client.
//  3. Registers event handlers.
//  4. Initialises the Python bridge with kernel callbacks.
//  5. Loads system modules from ModulesDir via SystemLoader.
//  6. Loads user modules from ModulesLoadedDir via UserLoader.
func (k *Kernel) Init() error {
	k.Log.Info("Initialising MCUB kernel (%s) v%s", k.Type, k.Version)

	// 1. Open database.
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

	// 2. Create Telegram client.
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

	// 3. Register event handlers.
	k.RegisterHandlers()
	k.Log.Info("Event handlers registered")

	// 4. Initialise Python bridge (shared between system and user loaders).
	bridge, bridgeErr := pybridge.NewBridge()
	if bridgeErr != nil {
		k.Log.Warn("Python bridge unavailable – .py modules will not load: %v", bridgeErr)
	} else {
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
		k.PyBridge = bridge
	}

	// 5. Load system modules from modules/ directory.
	sl := loader.NewSystemLoader(k, k.Loader, k.Log, k.PyBridge)
	sysLoaded, sysFailed, _ := sl.LoadSystemModules(k.ModulesDir)
	if len(sysFailed) > 0 {
		k.Log.Warn("System module load failures: %v", sysFailed)
	}
	k.Log.Info("System modules: %d loaded, %d failed", sysLoaded, len(sysFailed))

	// 6. Load user modules from modules_loaded/ directory.
	userLoaded, userFailed, _ := loader.LoadUserModules(k, k.ModulesLoadedDir)
	if len(userFailed) > 0 {
		k.Log.Warn("User module load failures: %v", userFailed)
	}
	k.Log.Info("User modules: %d loaded, %d failed", userLoaded, len(userFailed))

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


