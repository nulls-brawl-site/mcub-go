// SPDX-License-Identifier: MIT
// Port of modules/command.py from MCUB-fork.
//
// Python command.py is a ModuleBase subclass with ONLY @bot_command handlers:
//   /start, /profile, /init, /delete_mcub_bot, /ping, /mitrich
// and callbacks:
//   cb_language (language selection + backup setup)
//   cb_backup, cb_backup_interval, cb_backup_skip
//
// These require a running bot client, which the current Go kernel does not expose.
// The one userbot-facing command provided here (.setlang) is a functional equivalent
// of the cb_language callback: it lets the owner change the kernel language from the
// userbot account.

package modules

import (
	"context"
	"fmt"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// commandModule provides userbot-side language setup.
// Bot-side commands (/start, /profile, /init, /ping, /mitrich, /delete_mcub_bot)
// are defined in Python command.py as @bot_command and require a live bot client.
type commandModule struct {
	k *kernel.Kernel
}

func newCommandModule() loader.Module { return &commandModule{} }

// Name implements loader.Module.
func (m *commandModule) Name() string { return "command" }

// OnLoad implements loader.Module.
func (m *commandModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("command: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *commandModule) OnUnload(k interface{}) error {
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
// Python command.py exposes no userbot (.command) handlers; all commands are
// @bot_command.  The single .setlang command here mirrors what the cb_language
// callback does in Python (sets kernel language and saves config).
func (m *commandModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "setlang",
			Description: "Show or change the userbot language (equivalent to /init language callback)",
			Handler:     m.cmdSetlang,
		},
	}
}

// cmdSetlang shows the current language or sets a new one.
// Mirrors the Python cb_language callback which sets kernel.config["language"]
// and calls kernel.save_config().
func (m *commandModule) cmdSetlang(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev == nil || ev.Raw == nil {
		return nil
	}
	args := strings.TrimSpace(getArgsRaw(ev, m.k))
	if args == "" {
		// Python cb_language sends hello_installed + main_commands on setup.
		// Here we just show the current language since there's no inline bot.
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf(`🌐 <b>%s</b> <code>%s</code>`,
				s(m.k, "command", "kernel_version"),
				m.k.GetLanguage()))
	}
	lang := args
	m.k.Config.Language = lang
	if m.k.ConfigFile != "" {
		if err := m.k.Config.Save(m.k.ConfigFile); err != nil {
			m.k.Log.Warn("setlang: failed to save config: %v", err)
		}
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		sf(m.k, "settings", "lang_changed", map[string]interface{}{"lang": lang}))
}
