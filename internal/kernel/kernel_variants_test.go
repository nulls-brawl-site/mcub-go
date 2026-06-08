package kernel_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/config"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
)

func TestNewStandardKernel(t *testing.T) {
	cfg := &config.Config{CommandPrefix: ".", Aliases: make(map[string]string)}
	k := kernel.New(cfg, "test.json", kernel.KernelStandard)
	if k.Type != kernel.KernelStandard {
		t.Errorf("expected KernelStandard, got %s", k.Type)
	}
	if k.GetKernelTag() != "standard" {
		t.Errorf("expected tag 'standard', got %q", k.GetKernelTag())
	}
}

func TestNewZenKernel(t *testing.T) {
	cfg := &config.Config{CommandPrefix: ".", Aliases: make(map[string]string)}
	k := kernel.New(cfg, "test.json", kernel.KernelZen)
	if k.Type != kernel.KernelZen {
		t.Errorf("expected KernelZen, got %s", k.Type)
	}
	if k.GetKernelTag() != "zen" {
		t.Errorf("expected tag 'zen', got %q", k.GetKernelTag())
	}
	// Zen should have longer healthcheck intervals (60 s).
	if k.HealthcheckIntervalSec() != 60 {
		t.Errorf("expected HealthcheckIntervalSec=60, got %d", k.HealthcheckIntervalSec())
	}
}

func TestNewMiniKernel(t *testing.T) {
	cfg := &config.Config{CommandPrefix: ".", Aliases: make(map[string]string)}
	k := kernel.NewMiniKernel(cfg, "test.json")
	if k.Type != kernel.KernelMini {
		t.Errorf("expected KernelMini, got %s", k.Type)
	}
	if k.GetKernelTag() != "MINI" {
		t.Errorf("expected tag 'MINI', got %q", k.GetKernelTag())
	}
}

func TestNewBotKernel(t *testing.T) {
	cfg := &config.Config{CommandPrefix: ".", Aliases: make(map[string]string)}
	k := kernel.NewBotKernel(cfg, "test.json", "123:ABC")
	if k.Type != kernel.KernelBot {
		t.Errorf("expected KernelBot, got %s", k.Type)
	}
	if k.GetKernelTag() != "BOT" {
		t.Errorf("expected tag 'BOT', got %q", k.GetKernelTag())
	}
	if !k.IsBotMode {
		t.Error("expected IsBotMode=true")
	}
	if k.BotToken != "123:ABC" {
		t.Errorf("expected BotToken='123:ABC', got %q", k.BotToken)
	}
}
