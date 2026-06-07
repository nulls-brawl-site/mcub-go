package modules

import (
	"context"
	"fmt"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// configModule provides .cfg and .fcfg commands for managing module configs.
type configModule struct {
	k *kernel.Kernel
}

func newConfigModule() loader.Module { return &configModule{} }

// Name implements loader.Module.
func (m *configModule) Name() string { return "config" }

// OnLoad implements loader.Module.
func (m *configModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("config: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *configModule) OnUnload(k interface{}) error {
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
func (m *configModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "cfg", Description: "[module] [key] [value] — get/set module config", Handler: m.cmdCfg},
		{Name: "fcfg", Description: "[module] — show module config with all keys", Handler: m.cmdFcfg},
	}
}

// parseArgs strips the prefix+command and returns remaining tokens.
func (m *configModule) parseArgs(ev *events.NewMessage) []string {
	body := ev.Text()
	if m.k != nil {
		body = strings.TrimPrefix(body, m.k.Prefix())
	}
	parts := strings.Fields(body)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// parseArgsRaw returns the raw text after the command word.
func (m *configModule) parseArgsRaw(ev *events.NewMessage) string {
	body := ev.Text()
	if m.k != nil {
		body = strings.TrimPrefix(body, m.k.Prefix())
	}
	idx := strings.IndexByte(body, ' ')
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(body[idx+1:])
}

// ---------- .cfg ----------

// cmdCfg implements .cfg [module] [key] [value]
//
// No args → list all modules that have saved config.
// Module only → show all config keys for that module.
// Module + key → show the value of that key.
// Module + key + value → set the value.
func (m *configModule) cmdCfg(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	cfg := m.k.Config

	// No args → list modules.
	if len(args) == 0 {
		names := cfg.GetAllModuleNames()
		if len(names) == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"⚙️ <b>Config</b>\nNo module configs found.")
		}
		var sb strings.Builder
		sb.WriteString("⚙️ <b>Modules with config:</b>\n<blockquote expandable>")
		for _, n := range names {
			fmt.Fprintf(&sb, "• <code>%s</code>\n", n)
		}
		sb.WriteString("</blockquote>")
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
	}

	moduleName := args[0]

	// Module only → show all keys.
	if len(args) == 1 {
		return m.showModuleConfig(ctx, ev, moduleName)
	}

	key := args[1]

	// Module + key → show value.
	if len(args) == 2 {
		modCfg, err := cfg.GetModuleConfig(moduleName, nil)
		if err != nil || modCfg == nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚙️ No config found for module <code>%s</code>.", moduleName))
		}
		val, ok := modCfg[key]
		if !ok {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚙️ Key <code>%s</code> not found in <code>%s</code>.", key, moduleName))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("⚙️ <b>%s</b> → <code>%s</code> = <code>%v</code>",
				moduleName, key, val))
	}

	// Module + key + value → set value.
	// Raw value is everything after the key token.
	raw := m.parseArgsRaw(ev)
	// Strip "module key " prefix from raw to get value.
	raw = strings.TrimSpace(raw)
	// remove module name prefix
	if strings.HasPrefix(raw, moduleName) {
		raw = strings.TrimSpace(raw[len(moduleName):])
	}
	// remove key prefix
	if strings.HasPrefix(raw, key) {
		raw = strings.TrimSpace(raw[len(key):])
	}
	value := raw

	modCfg, err := cfg.GetModuleConfig(moduleName, map[string]interface{}{})
	if err != nil {
		modCfg = map[string]interface{}{}
	}
	if modCfg == nil {
		modCfg = map[string]interface{}{}
	}
	modCfg[key] = value
	if err := cfg.SetModuleConfig(moduleName, modCfg); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving config: %v", err))
	}
	if err := cfg.Save(m.k.ConfigFile); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error writing config file: %v", err))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("✅ <b>%s</b> → <code>%s</code> set to <code>%s</code>",
			moduleName, key, value))
}

// showModuleConfig shows all keys for a module config.
func (m *configModule) showModuleConfig(ctx context.Context, ev *events.NewMessage, moduleName string) error {
	modCfg, err := m.k.Config.GetModuleConfig(moduleName, nil)
	if err != nil || modCfg == nil || len(modCfg) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("⚙️ No config found for module <code>%s</code>.", moduleName))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "⚙️ <b>Config: %s</b>\n<blockquote expandable>\n", moduleName)
	for k, v := range modCfg {
		fmt.Fprintf(&sb, "• %s = <code>%v</code>\n", k, v)
	}
	sb.WriteString("</blockquote>")
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// ---------- .fcfg ----------

// cmdFcfg implements .fcfg [module] — full config view with all keys.
func (m *configModule) cmdFcfg(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	if len(args) == 0 {
		// Show all modules.
		names := m.k.Config.GetAllModuleNames()
		if len(names) == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"⚙️ <b>Full Config</b>\nNo module configs found.")
		}
		var sb strings.Builder
		sb.WriteString("⚙️ <b>Full Config — all modules:</b>\n")
		for _, name := range names {
			modCfg, err := m.k.Config.GetModuleConfig(name, nil)
			if err != nil || modCfg == nil {
				continue
			}
			fmt.Fprintf(&sb, "\n<b>%s</b>\n<blockquote expandable>\n", name)
			for k, v := range modCfg {
				fmt.Fprintf(&sb, "• %s = <code>%v</code>\n", k, v)
			}
			sb.WriteString("</blockquote>")
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
	}

	moduleName := args[0]
	return m.showModuleConfig(ctx, ev, moduleName)
}
