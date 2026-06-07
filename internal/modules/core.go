// Package modules contains the built-in system modules for MCUB.
package modules

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// coreModule provides essential system commands: ping, restart, info.
type coreModule struct {
	k *kernel.Kernel
}

func newCoreModule() *coreModule { return &coreModule{} }

// Name implements loader.Module.
func (m *coreModule) Name() string { return "core" }

// OnLoad implements loader.Module. It stores the kernel reference and
// registers all commands.
func (m *coreModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("core: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module. It unregisters all commands.
func (m *coreModule) OnUnload(k interface{}) error {
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

// Commands implements loader.Module.
func (m *coreModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "ping",
			Description: "Measure edit latency",
			Handler:     m.pingCmd,
		},
		{
			Name:        "restart",
			Description: "Restart the userbot process",
			Handler:     m.restartCmd,
		},
		{
			Name:        "info",
			Description: "Show userbot information (version, uptime, prefix, modules, Go version)",
			Handler:     m.infoCmd,
		},
	}
}

// pingCmd edits the triggering message twice and reports the round-trip edit latency.
func (m *coreModule) pingCmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}

	start := time.Now()
	_, err := m.k.Client.EditMessage(ctx, mcubclient.EditMessageParams{
		PeerID:    ev.PeerID,
		MessageID: ev.Raw.ID,
		Text:      "pinging...",
	})
	if err != nil {
		return fmt.Errorf("ping edit 1: %w", err)
	}

	elapsed := time.Since(start)
	_, err = m.k.Client.EditMessage(ctx, mcubclient.EditMessageParams{
		PeerID:    ev.PeerID,
		MessageID: ev.Raw.ID,
		Text:      fmt.Sprintf("pong! %dms", elapsed.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("ping edit 2: %w", err)
	}
	return nil
}

// restartCmd notifies the user and re-execs the current process.
func (m *coreModule) restartCmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	if m.k.Client != nil {
		_, _ = m.k.Client.EditMessage(ctx, mcubclient.EditMessageParams{
			PeerID:    ev.PeerID,
			MessageID: ev.Raw.ID,
			Text:      "restarting...",
		})
	}

	return m.k.Restart()
}

// infoCmd replies with a summary of the running userbot.
func (m *coreModule) infoCmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}

	totalModules := len(m.k.LoadedModules) + len(m.k.SystemModules)

	var sb strings.Builder
	sb.WriteString("MCUB Userbot\n")
	fmt.Fprintf(&sb, "Version : %s\n", m.k.Version)
	fmt.Fprintf(&sb, "Uptime  : %s\n", m.k.Uptime().Round(time.Second))
	fmt.Fprintf(&sb, "Prefix  : %s\n", m.k.Prefix())
	fmt.Fprintf(&sb, "Modules : %d\n", totalModules)
	fmt.Fprintf(&sb, "Go      : %s", runtime.Version())

	_, err := m.k.Client.EditMessage(ctx, mcubclient.EditMessageParams{
		PeerID:    ev.PeerID,
		MessageID: ev.Raw.ID,
		Text:      sb.String(),
	})
	return err
}
