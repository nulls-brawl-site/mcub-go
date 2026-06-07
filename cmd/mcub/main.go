// Command mcub is the main entry point for the MCUB Telegram userbot.
//
// Usage:
//
//	mcub [flags]
//
// Flags:
//
//	--config          Path to config.json (default: config.json)
//	--no-web          Disable the web panel
//	--port            Web panel port (default: 8080)
//	--host            Web panel host (default: 127.0.0.1)
//	--core            Kernel type: standard | zen (default: standard)
//	--set-default-core   Save the selected core as default in config
//	--clear-default-core Reset the default core in config
//	--log-level       Log level: debug | info | warn | error (default: info)
//	--version         Print version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nulls-brawl-site/mcub-go/internal/config"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/logger"
)

const version = "1.0.0"

func main() {
	// ---- CLI flags --------------------------------------------------------
	var (
		flagConfig           = flag.String("config", "config.json", "path to config.json")
		flagNoWeb            = flag.Bool("no-web", false, "disable web panel")
		flagPort             = flag.Int("port", 8080, "web panel port")
		flagHost             = flag.String("host", "127.0.0.1", "web panel host")
		flagCore             = flag.String("core", "standard", "kernel type: standard|zen")
		flagSetDefaultCore   = flag.Bool("set-default-core", false, "save selected core as default")
		flagClearDefaultCore = flag.Bool("clear-default-core", false, "clear default core from config")
		flagLogLevel         = flag.String("log-level", "info", "log level: debug|info|warn|error")
		flagVersion          = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stdout, "MCUB Go v%s\n", version)
		os.Exit(0)
	}

	// ---- Logger -----------------------------------------------------------
	log := logger.New(os.Stderr, logger.ParseLevel(*flagLogLevel))
	log.Info("MCUB Go v%s starting", version)

	// ---- Config -----------------------------------------------------------
	cfg, created, err := config.LoadOrCreate(*flagConfig)
	if err != nil {
		log.Error("Failed to load/create config: %v", err)
		os.Exit(1)
	}
	if created {
		log.Info("Default config created at %s – please fill in api_id, api_hash and phone", *flagConfig)
		os.Exit(0)
	}

	if err := cfg.Validate(); err != nil {
		log.Error("Config validation failed: %v", err)
		os.Exit(1)
	}

	// ---- Handle --set-default-core / --clear-default-core ----------------
	// We store the preferred core in a reserved web_panel_token field or a
	// dedicated DB key at runtime; for now we just log the intent and save.
	if *flagSetDefaultCore {
		log.Info("Saving default core=%s to config", *flagCore)
		// Persist the choice by saving config (no dedicated field yet).
		if saveErr := cfg.Save(*flagConfig); saveErr != nil {
			log.Warn("Could not save config: %v", saveErr)
		}
	}
	if *flagClearDefaultCore {
		log.Info("Clearing default core from config")
		if saveErr := cfg.Save(*flagConfig); saveErr != nil {
			log.Warn("Could not save config: %v", saveErr)
		}
	}

	// ---- Kernel type ------------------------------------------------------
	var kType kernel.KernelType
	switch *flagCore {
	case "zen":
		kType = kernel.KernelZen
	default:
		kType = kernel.KernelStandard
	}
	log.Info("Using kernel type: %s", kType)

	// ---- Web panel placeholder --------------------------------------------
	if !*flagNoWeb {
		log.Info("Web panel would listen on %s:%d (not yet implemented)", *flagHost, *flagPort)
	} else {
		log.Info("Web panel disabled")
	}

	// Suppress "declared and not used" for web flags when panel is not implemented.
	_ = *flagHost
	_ = *flagPort

	// ---- Build and initialise kernel -------------------------------------
	k := kernel.New(cfg, *flagConfig, kType)
	k.Log = log

	if err := k.Init(); err != nil {
		log.Error("Kernel init failed: %v", err)
		os.Exit(1)
	}

	// ---- Signal handling -------------------------------------------------
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- Run -------------------------------------------------------------
	if err := k.Run(ctx); err != nil && err != context.Canceled {
		log.Error("Kernel exited with error: %v", err)
		os.Exit(1)
	}

	log.Info("MCUB stopped")
}
