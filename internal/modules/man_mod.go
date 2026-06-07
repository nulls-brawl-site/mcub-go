// SPDX-License-Identifier: MIT
// Port of man.py → Go system module

package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// manModule implements .man, .manhide, .manunhide, .help.
type manModule struct {
	k *kernel.Kernel
}

func newManModule() loader.Module { return &manModule{} }

func (m *manModule) Name() string { return "man" }

func (m *manModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("man: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

func (m *manModule) OnUnload(k interface{}) error {
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

func (m *manModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "man", Description: "[name] show module info or list all modules", Handler: m.cmdMan},
		{Name: "manhide", Description: "<name> hide module from man list", Handler: m.cmdManhide},
		{Name: "manunhide", Description: "<name> unhide module from man list", Handler: m.cmdManunhide},
		{Name: "help", Description: "redirects to .man", Handler: m.cmdHelp},
	}
}

// ---------- DB helpers ----------

const manHiddenKey = "hidden_modules"

func (m *manModule) getHidden(ctx context.Context) ([]string, error) {
	if m.k == nil || m.k.DB == nil {
		return nil, nil
	}
	val, ok, err := m.k.DB.ModuleGet("man", manHiddenKey)
	if err != nil || !ok || val == "" {
		return nil, err
	}
	var result []string
	if jsonErr := json.Unmarshal([]byte(val), &result); jsonErr != nil {
		return nil, nil
	}
	return result, nil
}

func (m *manModule) saveHidden(ctx context.Context, hidden []string) error {
	if m.k == nil || m.k.DB == nil {
		return nil
	}
	data, err := json.Marshal(hidden)
	if err != nil {
		return err
	}
	return m.k.DB.ModuleSet("man", manHiddenKey, string(data))
}

// ---------- helpers ----------

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// getModuleCommands returns command names owned by a module.
func (m *manModule) getModuleCommands(modName string) []string {
	var cmds []string
	for cmdName, owner := range m.k.CommandOwners {
		if owner == modName {
			cmds = append(cmds, cmdName)
		}
	}
	sort.Strings(cmds)
	return cmds
}

// getCommandDesc returns the description for a command.
func (m *manModule) getCommandDesc(cmdName string) string {
	doc, ok := m.k.CommandDocs[cmdName]
	if ok {
		return doc.Description
	}
	return ""
}

// ---------- showModuleList ----------

func (m *manModule) showModuleList(ctx context.Context, ev *events.NewMessage) error {
	hidden, _ := m.getHidden(ctx)

	// Collect system module names
	sysNames := make([]string, 0, len(m.k.SystemModules))
	for name := range m.k.SystemModules {
		sysNames = append(sysNames, name)
	}
	userNames := make([]string, 0, len(m.k.LoadedModules))
	for name := range m.k.LoadedModules {
		userNames = append(userNames, name)
	}

	// Filter hidden
	var filteredSys []string
	for _, n := range sysNames {
		if !contains(hidden, n) {
			filteredSys = append(filteredSys, n)
		}
	}
	var filteredUser []string
	for _, n := range userNames {
		if !contains(hidden, n) {
			filteredUser = append(filteredUser, n)
		}
	}
	sort.Strings(filteredSys)
	sort.Strings(filteredUser)

	prefix := m.k.Prefix()
	var sb strings.Builder

	// System modules block
	sb.WriteString(fmt.Sprintf("🔮 <b>System modules:</b> <code>%d</code>\n", len(filteredSys)))
	sb.WriteString("<blockquote expandable>\n")
	for _, name := range filteredSys {
		cmds := m.getModuleCommands(name)
		cmdList := make([]string, 0, len(cmds))
		for _, c := range cmds {
			cmdList = append(cmdList, fmt.Sprintf("<code>%s%s</code>", html.EscapeString(prefix), html.EscapeString(c)))
		}
		sb.WriteString(fmt.Sprintf("▫️ <b>%s</b>: %s\n", html.EscapeString(name), strings.Join(cmdList, ", ")))
	}
	sb.WriteString("</blockquote>")

	// User modules block (only if any)
	if len(filteredUser) > 0 {
		sb.WriteString(fmt.Sprintf("\n🔮 <b>User modules: %d</b>\n", len(filteredUser)))
		sb.WriteString("<blockquote expandable>\n")
		for _, name := range filteredUser {
			cmds := m.getModuleCommands(name)
			cmdList := make([]string, 0, len(cmds))
			for _, c := range cmds {
				cmdList = append(cmdList, fmt.Sprintf("<code>%s%s</code>", html.EscapeString(prefix), html.EscapeString(c)))
			}
			sb.WriteString(fmt.Sprintf("▪️ <b>%s</b>: %s\n", html.EscapeString(name), strings.Join(cmdList, ", ")))
		}
		sb.WriteString("</blockquote>")
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// ---------- showModuleDetails ----------

func (m *manModule) showModuleDetails(ctx context.Context, ev *events.NewMessage, name string) error {
	// Attempt exact then prefix match across system + user modules
	_, isSys := m.k.SystemModules[name]
	_, isUser := m.k.LoadedModules[name]

	if !isSys && !isUser {
		// Try case-insensitive prefix match
		nameLower := strings.ToLower(name)
		for n := range m.k.SystemModules {
			if strings.HasPrefix(strings.ToLower(n), nameLower) {
				name = n
				isSys = true
				break
			}
		}
		if !isSys {
			for n := range m.k.LoadedModules {
				if strings.HasPrefix(strings.ToLower(n), nameLower) {
					name = n
					isUser = true
					break
				}
			}
		}
	}

	if !isSys && !isUser {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🚫 Module <code>%s</code> not found", html.EscapeString(name)))
	}

	prefix := m.k.Prefix()
	cmds := m.getModuleCommands(name)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("🧬 <b>Module</b> <code>%s</code>:\n", html.EscapeString(name)))
	sb.WriteString(fmt.Sprintf("⚗️ <b>Description:</b> <i>%s module</i>\n", html.EscapeString(name)))
	sb.WriteString("❄️ <b>Version:</b> <code>1.0.0</code>\n")
	sb.WriteString("<blockquote expandable>\n")
	for _, cmd := range cmds {
		desc := m.getCommandDesc(cmd)
		if desc == "" {
			desc = "no description"
		}
		sb.WriteString(fmt.Sprintf("🫧 <code>%s%s</code> - <b>%s</b>\n",
			html.EscapeString(prefix), html.EscapeString(cmd), html.EscapeString(desc)))
	}
	sb.WriteString("</blockquote>")
	sb.WriteString("\n<blockquote>🥞 <b>Author:</b> <i>@hairpin00</i></blockquote>")

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// ---------- command handlers ----------

func (m *manModule) cmdMan(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	args := ""
	if len(parts) >= 2 {
		args = strings.TrimSpace(parts[1])
	}

	if args == "" {
		return m.showModuleList(ctx, ev)
	}
	return m.showModuleDetails(ctx, ev, args)
}

func (m *manModule) cmdManhide(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			`🚫 <i>Usage: .manhide &lt;name&gt;</i>`)
	}
	name := strings.TrimSpace(parts[1])

	hidden, err := m.getHidden(ctx)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🚫 <i>DB error: %s</i>", html.EscapeString(err.Error())))
	}

	if contains(hidden, name) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("☑️ <i>Module</i> <code>%s</code> <i>is already hidden</i>", html.EscapeString(name)))
	}

	hidden = append(hidden, name)
	if err := m.saveHidden(ctx, hidden); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🚫 <i>DB error: %s</i>", html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("👁 <i>Module hidden:</i>\n<code>%s</code>", html.EscapeString(name)))
}

func (m *manModule) cmdManunhide(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			`🚫 <i>Usage: .manunhide &lt;name&gt;</i>`)
	}
	name := strings.TrimSpace(parts[1])

	hidden, err := m.getHidden(ctx)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🚫 <i>DB error: %s</i>", html.EscapeString(err.Error())))
	}

	if !contains(hidden, name) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("☑️ <i>Module</i> <code>%s</code> <i>is not hidden</i>", html.EscapeString(name)))
	}

	// Remove from hidden list
	newHidden := hidden[:0]
	for _, h := range hidden {
		if h != name {
			newHidden = append(newHidden, h)
		}
	}

	if err := m.saveHidden(ctx, newHidden); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🚫 <i>DB error: %s</i>", html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("☑️ <i>Module unhidden:</i>\n<code>%s</code>", html.EscapeString(name)))
}

func (m *manModule) cmdHelp(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	prefix := m.k.Prefix()
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("<b>Use</b> <code>%sman</code> <b>to see module list.</b>", html.EscapeString(prefix)))
}
