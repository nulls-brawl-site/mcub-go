// SPDX-License-Identifier: MIT
// Port of modules/command.py from MCUB-fork.
// Handles bot command helpers and userbot init flow.

package modules

import (
	"context"
	"fmt"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// commandModule provides bot command handlers and userbot init flow.
// Ported from modules/command.py – only the userbot-side commands are
// implemented here; the bot-side /start, /profile, /ping etc. require a live
// bot client which is outside the current Go scope.
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
func (m *commandModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "botsetup",
			Description: "Show inline bot configuration status",
			Handler:     m.cmdBotSetup,
		},
		{
			Name:        "setlang",
			Description: "Show or change the userbot language",
			Handler:     m.cmdSetlang,
		},
	}
}

// cmdBotSetup checks and shows the inline bot configuration status.
// Mirrors command.py cmdBotSetup / /init handling for the userbot side.
func (m *commandModule) cmdBotSetup(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev == nil || ev.Raw == nil {
		return nil
	}
	if m.k.Config.InlineBotToken == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			`⚙️ <b>Inline Bot</b>`+"\n"+
				`<blockquote>Not configured.`+"\n"+
				`Set <code>inline_bot_token</code> in config.json</blockquote>`)
	}
	token := *m.k.Config.InlineBotToken
	masked := token
	if len(token) > 15 {
		masked = token[:10] + "..." + token[len(token)-5:]
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf(`⚙️ <b>Inline Bot</b>`+"\n"+
			`<blockquote>Token: <code>%s</code>`+"\n"+
			`Status: Configured</blockquote>`, masked))
}

// cmdSetlang is a quick language-switcher shortcut.
// With no args it shows the current language; with an arg it sets it and saves
// config. Mirrors the language callback in command.py cb_language.
func (m *commandModule) cmdSetlang(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev == nil || ev.Raw == nil {
		return nil
	}
	args := strings.TrimSpace(getArgsRaw(ev, m.k))
	if args == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			`🌐 <b>Language:</b> <code>`+m.k.GetLanguage()+`</code>`+"\n"+
				`<blockquote>Available: en, ru, uk, de, es</blockquote>`)
	}
	lang := args
	m.k.Config.Language = lang
	if m.k.ConfigFile != "" {
		if err := m.k.Config.Save(m.k.ConfigFile); err != nil {
			m.k.Log.Warn("setlang: failed to save config: %v", err)
		}
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf(`✅ Language set to <code>%s</code>`, lang))
}
