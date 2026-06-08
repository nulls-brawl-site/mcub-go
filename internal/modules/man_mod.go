// SPDX-License-Identifier: MIT
// Port of man.py → Go system module

package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/langpacks"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji constants from man.py CUSTOM_EMOJI dict.
const (
	manEmojiCrystal   = `<tg-emoji emoji-id="5361837567463399422">🔮</tg-emoji>`
	manEmojiDNA       = `<tg-emoji emoji-id="5404451992456156919">🧬</tg-emoji>`
	manEmojiAlembic   = `<tg-emoji emoji-id="5379679518740978720">⚗️</tg-emoji>`
	manEmojiSnowflake = `<tg-emoji emoji-id="5431895003821513760">❄️</tg-emoji>`
	manEmojiBlocked   = `<tg-emoji emoji-id="5767151002666929821">🚫</tg-emoji>`
	manEmojiPancake   = `<tg-emoji emoji-id="5373004843210251169">🥞</tg-emoji>`
	manEmojiConfused  = `<tg-emoji emoji-id="5249119354825487565">🫨</tg-emoji>`
	manEmojiBubble    = `<tg-emoji emoji-id="5085121109574025951">🫧</tg-emoji>`
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

// ---------- langpack helpers ----------

func (m *manModule) lang() string {
	if m.k != nil {
		return m.k.GetLanguage()
	}
	return "en"
}

// s returns a localised string from the "man" module by key.
func (m *manModule) s(key string) string {
	return langpacks.Default.Get(m.lang(), "man", key)
}

// sf returns a localised string with Python-style {placeholder} substitution.
// pairs must be alternating: "{key}", "value", "{key2}", "value2", ...
func (m *manModule) sf(key string, pairs ...string) string {
	raw := langpacks.Default.Get(m.lang(), "man", key)
	if len(pairs) > 0 {
		return strings.NewReplacer(pairs...).Replace(raw)
	}
	return raw
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

func (m *manModule) showModuleList(ctx context.Context, ev *events.NewMessage, showHidden bool) error {
	hidden, _ := m.getHidden(ctx)

	// Collect system module names.
	sysNames := make([]string, 0, len(m.k.SystemModules))
	for name := range m.k.SystemModules {
		sysNames = append(sysNames, name)
	}
	userNames := make([]string, 0, len(m.k.LoadedModules))
	for name := range m.k.LoadedModules {
		userNames = append(userNames, name)
	}

	// Filter hidden (unless -f flag given).
	var filteredSys []string
	for _, n := range sysNames {
		if showHidden || !contains(hidden, n) {
			filteredSys = append(filteredSys, n)
		}
	}
	var filteredUser []string
	for _, n := range userNames {
		if showHidden || !contains(hidden, n) {
			filteredUser = append(filteredUser, n)
		}
	}
	sort.Strings(filteredSys)
	sort.Strings(filteredUser)

	prefix := m.k.Prefix()
	var sb strings.Builder

	// System modules block.
	// Format: {crystal} <b>{system_modules}:</b> <code>N</code><blockquote expandable>
	sb.WriteString(fmt.Sprintf("%s <b>%s:</b> <code>%d</code><blockquote expandable>\n",
		manEmojiCrystal, m.s("system_modules"), len(filteredSys)))
	for _, name := range filteredSys {
		cmds := m.getModuleCommands(name)
		cmdList := make([]string, 0, len(cmds))
		for _, c := range cmds {
			cmdList = append(cmdList, fmt.Sprintf("<code>%s%s</code>", html.EscapeString(prefix), html.EscapeString(c)))
		}
		sb.WriteString(fmt.Sprintf("▫️ <b>%s</b>: %s\n", html.EscapeString(name), strings.Join(cmdList, ", ")))
	}
	sb.WriteString("</blockquote>")

	// User modules block (only if any).
	if len(filteredUser) > 0 {
		// Format: {crystal} <b>{user_modules_page}:</b><blockquote expandable>
		userLabel := strings.NewReplacer(
			"{page}", "1",
			"{count}", fmt.Sprintf("%d", len(filteredUser)),
		).Replace(m.s("user_modules_page"))
		sb.WriteString(fmt.Sprintf("\n%s <b>%s:</b><blockquote expandable>\n",
			manEmojiCrystal, userLabel))
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

func (m *manModule) showModuleDetails(ctx context.Context, ev *events.NewMessage, searchTerm string, showHidden bool) error {
	hidden, _ := m.getHidden(ctx)
	nameLower := strings.ToLower(searchTerm)

	// Gather all visible modules.
	type modEntry struct {
		name string
		typ  string
	}
	var allMods []modEntry
	for n := range m.k.SystemModules {
		if showHidden || !contains(hidden, n) {
			allMods = append(allMods, modEntry{n, "system"})
		}
	}
	for n := range m.k.LoadedModules {
		if showHidden || !contains(hidden, n) {
			allMods = append(allMods, modEntry{n, "user"})
		}
	}

	// Exact match first.
	var exactMatch *modEntry
	for i := range allMods {
		if strings.ToLower(allMods[i].name) == nameLower {
			exactMatch = &allMods[i]
			break
		}
	}

	if exactMatch == nil {
		// Substring matches against module names and command names.
		seen := map[string]bool{}
		var similar []modEntry
		for _, e := range allMods {
			if strings.Contains(strings.ToLower(e.name), nameLower) {
				if !seen[e.name] {
					similar = append(similar, e)
					seen[e.name] = true
				}
			} else {
				for cmdName, owner := range m.k.CommandOwners {
					if owner == e.name && strings.Contains(strings.ToLower(cmdName), nameLower) {
						if !seen[e.name] {
							similar = append(similar, e)
							seen[e.name] = true
						}
						break
					}
				}
			}
		}
		if len(similar) == 1 {
			exactMatch = &similar[0]
		} else if len(similar) > 1 {
			// Show a list of matching modules (Python-style "found_modules").
			prefix := m.k.Prefix()
			msg := fmt.Sprintf("%s <b>%s:</b>\n<blockquote expandable>", manEmojiCrystal, m.s("found_modules"))
			for i, e := range similar {
				if i >= 5 {
					break
				}
				cmds := m.getModuleCommands(e.name)
				cmdText := ""
				if len(cmds) > 0 {
					top := cmds
					if len(top) > 2 {
						top = top[:2]
					}
					parts := make([]string, 0, len(top))
					for _, c := range top {
						parts = append(parts, fmt.Sprintf("<code>%s%s</code>", html.EscapeString(prefix), html.EscapeString(c)))
					}
					cmdText = strings.Join(parts, ", ")
				}
				msg += fmt.Sprintf("<b>%s</b>: %s\n", html.EscapeString(e.name), cmdText)
			}
			msg += "</blockquote>"
			if len(similar) > 5 {
				msg += m.sf("and_more", "{count}", strconv.Itoa(len(similar)-5)) + "\n"
			}
			msg += fmt.Sprintf("\n<blockquote><i>%s</i> %s</blockquote>", m.s("no_exact_match"), manEmojiBlocked)
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, msg)
		}
	}

	if exactMatch == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("<blockquote expandable>%s %s</blockquote>",
				manEmojiBlocked, m.s("module_not_found")))
	}

	name := exactMatch.name
	typ := exactMatch.typ
	_ = typ

	prefix := m.k.Prefix()
	cmds := m.getModuleCommands(name)

	// Format matching Python _build_module_detail:
	// {dna} <b>{module}</b> <code>name</code>:
	// {alembic} <b>{description}:</b> <i>desc</i>
	// {snowflake} <b>{version}:</b> <code>ver</code>
	// <blockquote expandable>
	// {bubble} <code>.cmd</code> - <b>desc</b>
	// </blockquote>
	// <blockquote>{pancake} <b>{author}:</b> <i>@author</i></blockquote>
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>%s</b> <code>%s</code>:\n",
		manEmojiDNA, m.s("module"), html.EscapeString(name)))
	sb.WriteString(fmt.Sprintf("%s <b>%s:</b> <i>%s</i>\n",
		manEmojiAlembic, m.s("description"), m.s("no_description")))
	sb.WriteString(fmt.Sprintf("%s <b>%s:</b> <code>1.0.0</code>\n",
		manEmojiSnowflake, m.s("version")))
	sb.WriteString("<blockquote expandable>")
	if len(cmds) > 0 {
		for _, cmd := range cmds {
			desc := m.getCommandDesc(cmd)
			if desc == "" {
				desc = m.s("no_description")
			}
			sb.WriteString(fmt.Sprintf("%s <code>%s%s</code> - <b>%s</b>\n",
				manEmojiBubble,
				html.EscapeString(prefix), html.EscapeString(cmd),
				html.EscapeString(desc)))
		}
	} else {
		sb.WriteString(fmt.Sprintf("%s %s\n", manEmojiBlocked, m.s("no_commands")))
	}
	sb.WriteString("</blockquote>")
	sb.WriteString(fmt.Sprintf("\n<blockquote>%s <b>%s:</b> <i>%s</i></blockquote>",
		manEmojiPancake, m.s("author"), m.s("unknown")))

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// ---------- command handlers ----------

func (m *manModule) cmdMan(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	rawArgs := ""
	if len(parts) >= 2 {
		rawArgs = strings.TrimSpace(parts[1])
	}

	// Parse args — strip -f flag (show hidden modules).
	argList := strings.Fields(rawArgs)
	showHidden := false
	var cleanArgs []string
	for _, a := range argList {
		if a == "-f" {
			showHidden = true
		} else {
			cleanArgs = append(cleanArgs, a)
		}
	}

	if len(cleanArgs) == 0 {
		return m.showModuleList(ctx, ev, showHidden)
	}
	return m.showModuleDetails(ctx, ev, strings.Join(cleanArgs, " "), showHidden)
}

func (m *manModule) cmdManhide(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("manhide_usage"))
	}
	name := strings.TrimSpace(parts[1])

	// Resolve module name: exact match first, then fuzzy.
	allMods := map[string]bool{}
	for n := range m.k.SystemModules {
		allMods[n] = true
	}
	for n := range m.k.LoadedModules {
		allMods[n] = true
	}
	if !allMods[name] {
		nameLower := strings.ToLower(name)
		var matches []string
		for n := range allMods {
			if strings.Contains(strings.ToLower(n), nameLower) {
				matches = append(matches, n)
			}
		}
		if len(matches) == 1 {
			name = matches[0]
		} else {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("%s %s", manEmojiBlocked, m.s("module_not_found")))
		}
	}

	hidden, err := m.getHidden(ctx)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>DB error: %s</i>", manEmojiBlocked, html.EscapeString(err.Error())))
	}

	if contains(hidden, name) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("module_already_hidden"))
	}

	hidden = append(hidden, name)
	if err := m.saveHidden(ctx, hidden); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>DB error: %s</i>", manEmojiBlocked, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s\n<code>%s</code>", m.s("module_hidden"), html.EscapeString(name)))
}

func (m *manModule) cmdManunhide(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("manunhide_usage"))
	}
	name := strings.TrimSpace(parts[1])

	hidden, err := m.getHidden(ctx)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>DB error: %s</i>", manEmojiBlocked, html.EscapeString(err.Error())))
	}

	if !contains(hidden, name) {
		// Fuzzy match within the hidden list.
		nameLower := strings.ToLower(name)
		var matches []string
		for _, h := range hidden {
			if strings.Contains(strings.ToLower(h), nameLower) {
				matches = append(matches, h)
			}
		}
		if len(matches) == 1 {
			name = matches[0]
		} else {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("module_not_hidden"))
		}
	}

	// Remove from hidden list.
	var newHidden []string
	for _, h := range hidden {
		if h != name {
			newHidden = append(newHidden, h)
		}
	}

	if err := m.saveHidden(ctx, newHidden); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>DB error: %s</i>", manEmojiBlocked, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s\n<code>%s</code>", m.s("module_unhidden"), html.EscapeString(name)))
}

func (m *manModule) cmdHelp(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	prefix := m.k.Prefix()
	// Python: f"<b>{self.strings['help_not_command']}</b><code>{self.kernel.custom_prefix}man?</code>"
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("<b>%s</b><code>%sman?</code>",
			html.EscapeString(m.s("help_not_command")),
			html.EscapeString(prefix)))
}
