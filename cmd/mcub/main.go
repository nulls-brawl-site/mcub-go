// Command mcub is the main entry point for the MCUB Telegram userbot.
//
// Usage:
//
//	mcub [flags]
//
// Flags:
//
//	--config             Path to config.json (default: config.json)
//	--no-web             Disable the web panel
//	--port               Web panel port (default: 8080)
//	--host               Web panel host (default: 127.0.0.1)
//	--proxy-web          Enable web proxy at path
//	--core               Kernel type: standard | zen | mini (default: standard)
//	--bot-token          Run as a bot with this token (activates bot kernel)
//	--mini               Run in mini (lightweight) mode
//	--set-default-core   Save the selected core as default in config
//	--clear-default-core Reset the default core in config
//	--log-level          Log level: debug | info | warn | error (default: info)
//	--version            Print version and exit
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nulls-brawl-site/mcub-go/internal/colors"
	"github.com/nulls-brawl-site/mcub-go/internal/config"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/logger"
	"github.com/nulls-brawl-site/mcub-go/internal/modules"
	"github.com/nulls-brawl-site/mcub-go/internal/version"
	"github.com/nulls-brawl-site/mcub-go/internal/web"
)

func main() {
	// ---- CLI flags --------------------------------------------------------
	var (
		flagConfig           = flag.String("config", "config.json", "path to config.json")
		flagNoWeb            = flag.Bool("no-web", false, "disable web panel")
		flagPort             = flag.Int("port", 8080, "web panel port")
		flagHost             = flag.String("host", "127.0.0.1", "web panel host")
		flagProxyWeb         = flag.String("proxy-web", "", "enable web proxy at path")
		flagCore             = flag.String("core", "standard", "kernel type: standard|zen|mini")
		flagBotToken         = flag.String("bot-token", "", "run as bot with this token (activates bot kernel)")
		flagMini             = flag.Bool("mini", false, "run in mini (lightweight) mode")
		flagSetDefaultCore   = flag.Bool("set-default-core", false, "save selected core as default")
		flagClearDefaultCore = flag.Bool("clear-default-core", false, "clear default core from config")
		flagLogLevel         = flag.String("log-level", "info", "log level: debug|info|warn|error")
		flagVersion          = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *flagVersion {
		fmt.Fprintf(os.Stdout, "MCUB-Go %s\n", version.GetVersion())
		return
	}

	// ---- Logger -----------------------------------------------------------
	log := logger.New(os.Stderr, logger.ParseLevel(*flagLogLevel))

	// ---- Config -----------------------------------------------------------
	cfg, created, err := config.LoadOrCreate(*flagConfig)
	if err != nil {
		log.Error("Failed to load/create config: %v", err)
		os.Exit(1)
	}
	if created {
		log.Info("Default config created at %s – please fill in api_id, api_hash and phone, then restart.", *flagConfig)
		return
	}

	if err := cfg.Validate(); err != nil {
		log.Error("Config validation failed: %v", err)
		os.Exit(1)
	}

	// ---- Handle --set-default-core / --clear-default-core ----------------
	if *flagSetDefaultCore {
		log.Info("Saving default core=%s to config", *flagCore)
		if saveErr := cfg.Save(*flagConfig); saveErr != nil {
			log.Warn("Could not save config: %v", saveErr)
		}
		return
	}
	if *flagClearDefaultCore {
		log.Info("Clearing default core from config")
		if saveErr := cfg.Save(*flagConfig); saveErr != nil {
			log.Warn("Could not save config: %v", saveErr)
		}
		return
	}

	// ---- Build kernel -----------------------------------------------------
	var k *kernel.Kernel
	switch {
	case *flagBotToken != "":
		k = kernel.NewBotKernel(cfg, *flagConfig, *flagBotToken)
		log.Info("MCUB-Go %s starting (kernel: bot)", version.GetVersion())
	case *flagMini || *flagCore == "mini":
		k = kernel.NewMiniKernel(cfg, *flagConfig)
		log.Info("MCUB-Go %s starting (kernel: mini)", version.GetVersion())
	case *flagCore == "zen":
		k = kernel.New(cfg, *flagConfig, kernel.KernelZen)
		log.Info("MCUB-Go %s starting (kernel: zen)", version.GetVersion())
	default:
		k = kernel.New(cfg, *flagConfig, kernel.KernelStandard)
		log.Info("MCUB-Go %s starting (kernel: standard)", version.GetVersion())
	}
	k.Log = log

	// ---- Load system modules ----------------------------------------------
	for _, m := range modules.AllSystemModules() {
		if err := k.Loader.LoadBuiltin(m); err != nil {
			log.Warn("Failed to load system module %s: %v", m.Name(), err)
		}
	}

	// ---- Web panel --------------------------------------------------------
	// Mini and bot kernels skip the web panel (matches mini.py / bot.py behaviour).
	skipWeb := *flagNoWeb || k.Type == kernel.KernelMini || k.Type == kernel.KernelBot
	if !skipWeb {
		webPassword := ""
		if cfg.WebPanelToken != nil {
			webPassword = *cfg.WebPanelToken
		}
		srv := web.New(k, *flagHost, *flagPort, webPassword)
		go func() {
			log.Info("Web panel listening on http://%s:%d%s", *flagHost, *flagPort, *flagProxyWeb)
			ctx := context.Background()
			if err := srv.Start(ctx); err != nil {
				log.Warn("Web panel error: %v", err)
			}
		}()
	} else {
		if k.Type == kernel.KernelMini || k.Type == kernel.KernelBot {
			log.Info("Web panel skipped (kernel: %s)", k.GetKernelTag())
		} else {
			log.Info("Web panel disabled")
		}
	}

	// ---- Print startup banner ---------------------------------------------
	printBanner(k)

	// ---- Signal handling --------------------------------------------------
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- Init and run kernel ----------------------------------------------
	if err := k.Init(); err != nil {
		log.Error("Kernel init failed: %v", err)
		os.Exit(1)
	}

	if err := k.Run(ctx); err != nil && err != context.Canceled {
		log.Error("Kernel exited with error: %v", err)
		os.Exit(1)
	}

	log.Info("MCUB stopped")
}

// printBanner prints the MCUB ASCII art banner to stdout.
func printBanner(k *kernel.Kernel) {
	art := ` _    _  ____ _   _ ____
| \  / |/ ___| | | | __ )
| |\/| | |   | | | |  _ \
| |  | | |___| |_| | |_) |
|_|  |_|\____|\___/|____/`

	stops := [][3]int{{200, 0, 0}, {230, 60, 0}, {255, 140, 0}, {220, 220, 220}}
	colored := colors.GradientMulticolor(art, stops, false, true)
	fmt.Println(colored)
	fmt.Printf("Kernel: %s | Version: %s | Prefix: %s\n\n",
		k.Type, k.Version, k.CustomPrefix)
}
