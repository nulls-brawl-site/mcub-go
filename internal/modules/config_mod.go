package modules

import (
	"context"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/langpacks"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// configModule provides .cfg and .fcfg commands for managing module configs.
// It is a Go port of config.py (register(kernel) function in Python).
// Python uses inline queries; Go uses text commands.
type configModule struct {
	k *kernel.Kernel
}

// ---------- langpack helpers ----------

func (m *configModule) lang() string {
	if m.k != nil {
		return m.k.GetLanguage()
	}
	return "en"
}

func (m *configModule) t(key string) string {
	return langpacks.Default.Get(m.lang(), "config", key)
}

func (m *configModule) tf(key string, pairs ...string) string {
	raw := langpacks.Default.Get(m.lang(), "config", key)
	if len(pairs) > 0 {
		return strings.NewReplacer(pairs...).Replace(raw)
	}
	return raw
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
		{Name: "cfg", Description: "[module] [key] [value] — get/set module/kernel config", Handler: m.cmdCfg},
		{Name: "fcfg", Description: "[module] [key] [value] — full config view / set value", Handler: m.cmdFcfg},
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
// No args → list all modules that have saved config (mirrors Python config_menu_handler).
// Module only → show all config keys for that module.
// Module + key → show the value of that key.
// Module + key + value → set the value.
func (m *configModule) cmdCfg(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	cfg := m.k.Config

	// No args → list modules (mirrors Python config_menu_handler).
	if len(args) == 0 {
		names := cfg.GetAllModuleNames()
		// Sort for stable output.
		sort.Strings(names)
		var sb strings.Builder
		menuEmoji := `<tg-emoji emoji-id="5431736674147114227">📋</tg-emoji>`
		sb.WriteString(fmt.Sprintf("%s <b>Config Menu</b>\n<blockquote expandable>", menuEmoji))
		if len(names) == 0 {
			sb.WriteString(m.t("no_config"))
		} else {
			for _, n := range names {
				fmt.Fprintf(&sb, "• <code>%s</code>\n", html.EscapeString(n))
			}
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

	// Module + key → show value (mirrors Python module key view).
	if len(args) == 2 {
		modCfg, err := cfg.GetModuleConfig(moduleName, nil)
		if err != nil || modCfg == nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚙️ %s: <code>%s</code>.", m.t("no_config"), html.EscapeString(moduleName)))
		}
		val, ok := modCfg[key]
		if !ok {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				m.tf("key_not_found",
					"{ballot}", `<tg-emoji emoji-id="5359741159566484212">🗳</tg-emoji>`,
					"{key}", html.EscapeString(key)))
		}
		// Format using key_view template.
		valType := fmt.Sprintf("%T", val)
		valType = strings.TrimPrefix(valType, "interface {}")
		if valType == "" {
			valType = "string"
		}
		displayVal := fmt.Sprintf("<code>%s</code>", html.EscapeString(fmt.Sprintf("%v", val)))
		typeEmoji := `📝`
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.tf("key_view",
				"{note}", `<tg-emoji emoji-id="5334882760735598374">📝</tg-emoji>`,
				"{key}", html.EscapeString(key),
				"{type_emoji}", typeEmoji,
				"{value_type}", html.EscapeString(valType),
				"{display_value}", displayVal))
	}

	// Module + key + value → set value.
	raw := m.parseArgsRaw(ev)
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, moduleName) {
		raw = strings.TrimSpace(raw[len(moduleName):])
	}
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
			fmt.Sprintf("❌ %s: %v", m.t("error"), err))
	}
	if err := cfg.Save(m.k.ConfigFile); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error writing config file: %v", err))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.tf("fcfg_confirm_success",
			"{key}", html.EscapeString(key),
			"{value}", html.EscapeString(value)))
}

// showModuleConfig shows all keys for a module config (mirrors Python show_module_config_view).
func (m *configModule) showModuleConfig(ctx context.Context, ev *events.NewMessage, moduleName string) error {
	modCfg, err := m.k.Config.GetModuleConfig(moduleName, nil)
	if err != nil || modCfg == nil || len(modCfg) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s: <code>%s</code>", m.t("no_config"), html.EscapeString(moduleName)))
	}

	// Sort keys for stable output.
	keys := make([]string, 0, len(modCfg))
	for k := range modCfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	puzzleEmoji := `<tg-emoji emoji-id="5359785904535774578">🧩</tg-emoji>`
	pageEmoji := `<tg-emoji emoji-id="5433982607035474385">📰</tg-emoji>`
	var sb strings.Builder
	// Use module_config_title format.
	title := m.tf("module_config_title",
		"{puzzle}", puzzleEmoji,
		"{module_name}", html.EscapeString(moduleName),
		"{page_emoji}", pageEmoji,
		"{page}", "1",
		"{total_pages}", "1",
		"{total_items}", fmt.Sprintf("%d", len(keys)))
	sb.WriteString(title)
	sb.WriteString("\n<blockquote expandable>\n")
	for _, k := range keys {
		v := modCfg[k]
		fmt.Fprintf(&sb, "• <code>%s</code> = <code>%s</code>\n",
			html.EscapeString(k), html.EscapeString(fmt.Sprintf("%v", v)))
	}
	sb.WriteString("</blockquote>")
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// ---------- .fcfg ----------

// cmdFcfg implements .fcfg [module] [key] [value] — full config view / set value.
// Mirrors Python fcfg_inline_handler behaviour via text commands.
func (m *configModule) cmdFcfg(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	if len(args) == 0 {
		// Show all modules — mirrors Python config_modules_handler.
		names := m.k.Config.GetAllModuleNames()
		sort.Strings(names)
		puzzleEmoji := `<tg-emoji emoji-id="5359785904535774578">🧩</tg-emoji>`
		pageEmoji := `<tg-emoji emoji-id="5433982607035474385">📰</tg-emoji>`
		title := m.tf("modules_config_title",
			"{puzzle}", puzzleEmoji,
			"{page_emoji}", pageEmoji,
			"{page}", "1",
			"{total_pages}", "1",
			"{total_modules}", fmt.Sprintf("%d", len(names)))
		var sb strings.Builder
		sb.WriteString(title)
		sb.WriteString("\n<blockquote expandable>\n")
		if len(names) == 0 {
			sb.WriteString(m.t("no_config"))
		} else {
			for _, n := range names {
				fmt.Fprintf(&sb, "• <code>%s</code>\n", html.EscapeString(n))
			}
		}
		sb.WriteString("</blockquote>")
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
	}

	moduleName := args[0]

	// Module only → show all keys.
	if len(args) == 1 {
		return m.showModuleConfig(ctx, ev, moduleName)
	}

	// Module + key → show value.
	key := args[1]
	if len(args) == 2 {
		return m.cmdCfg(ctx, ev) // Delegate to cmdCfg which handles this case.
	}

	// Module + key + value → set value (mirrors Python fcfg set action).
	raw := m.parseArgsRaw(ev)
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, moduleName) {
		raw = strings.TrimSpace(raw[len(moduleName):])
	}
	if strings.HasPrefix(raw, key) {
		raw = strings.TrimSpace(raw[len(key):])
	}
	value := raw

	modCfg, err := m.k.Config.GetModuleConfig(moduleName, map[string]interface{}{})
	if err != nil {
		modCfg = map[string]interface{}{}
	}
	if modCfg == nil {
		modCfg = map[string]interface{}{}
	}
	modCfg[key] = value
	if err := m.k.Config.SetModuleConfig(moduleName, modCfg); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ %s: %v", m.t("error"), err))
	}
	if err := m.k.Config.Save(m.k.ConfigFile); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error writing config file: %v", err))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.tf("fcfg_confirm_success",
			"{key}", html.EscapeString(key),
			"{value}", html.EscapeString(value)))
}
