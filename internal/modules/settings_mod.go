// SPDX-License-Identifier: MIT
// Port of modules/settings.py from MCUB-fork.

package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/langpacks"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// settingsAvailableLangs is the list of supported language codes.
var settingsAvailableLangs = []string{"en", "ru", "uk", "de", "es"}

// settingsModule implements the "settings" system module.
type settingsModule struct {
	k *kernel.Kernel
}

func newSettingsModule() loader.Module { return &settingsModule{} }

// Name implements loader.Module.
func (m *settingsModule) Name() string { return "settings" }

// OnLoad implements loader.Module.
func (m *settingsModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("settings: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *settingsModule) OnUnload(k interface{}) error {
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
func (m *settingsModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "setprefix", Description: "[prefix] [id/@username/reply] change owner prefix", Handler: m.cmdSetPrefix},
		{Name: "addalias", Description: "[alias]=[cmd] add command alias", Handler: m.cmdAddAlias},
		{Name: "delalias", Description: "[alias] delete command alias", Handler: m.cmdDelAlias},
		{Name: "aliases", Description: "list all command aliases", Handler: m.cmdAliases},
		{Name: "iloadalias", Description: "[url/reply] import aliases from JSON", Handler: m.cmdIloadAlias},
		{Name: "unla", Description: "export aliases to JSON file", Handler: m.cmdUnla},
		{Name: "lang", Description: "[ru/en/...] change bot language", Handler: m.cmdLang},
		{Name: "cleardb", Description: "delete database file (add --yes to confirm)", Handler: m.cmdClearDB},
		{Name: "clearmodules", Description: "delete all user modules (add --yes to confirm)", Handler: m.cmdClearModules},
		{Name: "clearcache", Description: "clear kernel cache (add --yes to confirm)", Handler: m.cmdClearCache},
		{Name: "mcubinfo", Description: "what is a userbot", Handler: m.cmdMcubInfo},
		{Name: "piped", Description: "[on/off] toggle pipeline mode", Handler: m.cmdPiped},
		{Name: "mcub", Description: "show MCUB version info", Handler: m.cmdMcub},
	}
}

// ---------- langpack helpers ----------

func (m *settingsModule) lang() string {
	if m.k != nil {
		return m.k.GetLanguage()
	}
	return "en"
}

// s returns a localised string from the "settings" module by key.
func (m *settingsModule) s(key string) string {
	return langpacks.Default.Get(m.lang(), "settings", key)
}

// sf returns a localised string with Python-style {placeholder} substitution.
// pairs must be alternating: "{key}", "value", "{key2}", "value2", ...
func (m *settingsModule) sf(key string, pairs ...string) string {
	raw := langpacks.Default.Get(m.lang(), "settings", key)
	if len(pairs) > 0 {
		return strings.NewReplacer(pairs...).Replace(raw)
	}
	return raw
}

// ---------- helpers ----------

// getArgsRaw returns everything after the first command word in the event text,
// stripping the kernel prefix.
func getArgsRaw(ev *events.NewMessage, k *kernel.Kernel) string {
	text := ev.Text()
	if k != nil {
		text = strings.TrimPrefix(text, k.Prefix())
	}
	idx := strings.IndexAny(text, " \t\n")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(text[idx+1:])
}

// hasFlag checks whether args contain a given flag like "--yes".
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if strings.EqualFold(a, flag) {
			return true
		}
	}
	return false
}

// saveConfig saves the kernel config to disk.
func (m *settingsModule) saveConfig() error {
	if m.k == nil || m.k.Config == nil {
		return nil
	}
	return m.k.Config.Save(m.k.ConfigFile)
}

// resolvePrefixTarget resolves the target for setprefix.
// Returns 0 on failure.
func (m *settingsModule) resolvePrefixTarget(ctx context.Context, ev *events.NewMessage, target string) (int64, error) {
	target = strings.TrimSpace(target)
	switch {
	case strings.EqualFold(target, "reply"):
		if ev.ReplyToMsgID == 0 {
			return 0, fmt.Errorf("not a reply")
		}
		msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
		if err != nil || len(msgs) == 0 || msgs[0] == nil {
			return 0, fmt.Errorf("could not get reply message")
		}
		peer, ok := msgs[0].FromID.(*tg.PeerUser)
		if !ok {
			return 0, fmt.Errorf("reply sender is not a user")
		}
		return peer.UserID, nil

	case strings.HasPrefix(target, "@"):
		entity, err := m.k.Client.GetEntity(ctx, target[1:])
		if err != nil {
			return 0, fmt.Errorf("could not resolve %s: %w", target, err)
		}
		u, ok := entity.(*tg.User)
		if !ok {
			return 0, fmt.Errorf("target is not a user")
		}
		return u.ID, nil

	default:
		n, err := strconv.ParseInt(strings.TrimPrefix(target, "-"), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid target: %s", target)
		}
		if strings.HasPrefix(target, "-") {
			n = -n
		}
		return n, nil
	}
}

// ---------- .setprefix ----------

func (m *settingsModule) cmdSetPrefix(ctx context.Context, ev *events.NewMessage) error {
	prefix := m.k.Prefix()
	raw := getArgsRaw(ev, m.k)
	args := strings.Fields(raw)

	if len(args) < 1 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("prefix_usage", "{prefix}", html.EscapeString(prefix)))
	}

	newPrefix := args[0]
	if len([]rune(newPrefix)) != 1 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("prefix_one_char"))
	}

	var targetID int64
	if len(args) > 1 {
		id, err := m.resolvePrefixTarget(ctx, ev, args[1])
		if err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("prefix_target_invalid"))
		}
		targetID = id
	} else if ev.ReplyToMsgID != 0 {
		msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
		if err == nil && len(msgs) > 0 && msgs[0] != nil {
			if peer, ok := msgs[0].FromID.(*tg.PeerUser); ok {
				targetID = peer.UserID
			}
		}
	}
	if targetID == 0 {
		targetID = ev.SenderID
	}

	key := strconv.FormatInt(targetID, 10)
	oldPrefix := m.k.Config.CommandPrefix
	if m.k.Config.OwnerPrefixes != nil {
		if p, ok := m.k.Config.OwnerPrefixes[key]; ok {
			oldPrefix = p
		}
	}
	if m.k.Config.OwnerPrefixes == nil {
		m.k.Config.OwnerPrefixes = make(map[string]string)
	}
	m.k.Config.OwnerPrefixes[key] = newPrefix

	if targetID == m.k.AdminID {
		m.k.SetPrefix(newPrefix)
		m.k.Config.CommandPrefix = newPrefix
	}

	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving config: %s", html.EscapeString(err.Error())))
	}

	if targetID == m.k.AdminID {
		// Use prefix_changed for own prefix.
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("prefix_changed",
				"{prefix}", html.EscapeString(newPrefix),
				"{prefix_old}", html.EscapeString(oldPrefix)))
	}
	// Use prefix_owner_changed for others.
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.sf("prefix_owner_changed",
			"{owner_id}", html.EscapeString(key),
			"{prefix_old}", html.EscapeString(oldPrefix),
			"{prefix}", html.EscapeString(newPrefix)))
}

// ---------- .addalias ----------

func (m *settingsModule) cmdAddAlias(ctx context.Context, ev *events.NewMessage) error {
	raw := getArgsRaw(ev, m.k)
	prefix := m.k.Prefix()

	if !strings.Contains(raw, "=") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("alias_usage", "{prefix}", html.EscapeString(prefix)))
	}

	parts := strings.SplitN(raw, "=", 2)
	alias := strings.TrimSpace(parts[0])
	command := strings.TrimSpace(parts[1])

	if alias == "" || command == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("alias_usage", "{prefix}", html.EscapeString(prefix)))
	}

	// Check command exists.
	cmdBase := strings.Fields(command)[0]
	if _, ok := m.k.CommandHandlers[cmdBase]; !ok {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("alias_target_not_found", "{command}", html.EscapeString(cmdBase)))
	}

	m.k.AddAlias(alias, command)
	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving config: %s", html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.sf("alias_created",
			"{prefix}", html.EscapeString(prefix),
			"{alias}", html.EscapeString(alias),
			"{command}", html.EscapeString(command)))
}

// ---------- .delalias ----------

func (m *settingsModule) cmdDelAlias(ctx context.Context, ev *events.NewMessage) error {
	args := strings.TrimSpace(getArgsRaw(ev, m.k))
	prefix := m.k.Prefix()

	if args == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("delalias_usage", "{prefix}", html.EscapeString(prefix)))
	}

	if _, ok := m.k.Aliases[args]; !ok {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("delalias_not_found", "{alias}", html.EscapeString(args)))
	}

	m.k.RemoveAlias(args)
	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving config: %s", html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.sf("delalias_done",
			"{prefix}", html.EscapeString(prefix),
			"{alias}", html.EscapeString(args)))
}

// ---------- .aliases ----------

func (m *settingsModule) cmdAliases(ctx context.Context, ev *events.NewMessage) error {
	if len(m.k.Aliases) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("aliases_empty"))
	}

	prefix := m.k.Prefix()
	keys := make([]string, 0, len(m.k.Aliases))
	for k := range m.k.Aliases {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var lines []string
	for _, alias := range keys {
		target := m.k.Aliases[alias]
		lines = append(lines,
			fmt.Sprintf("<code>%s%s </code>→<code> %s%s</code>",
				html.EscapeString(prefix), html.EscapeString(alias),
				html.EscapeString(prefix), html.EscapeString(target)))
	}

	body := strings.Join(lines, "\n")
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("<blockquote expandable>%s</blockquote>", body))
}

// ---------- .iloadalias ----------

func (m *settingsModule) cmdIloadAlias(ctx context.Context, ev *events.NewMessage) error {
	args := strings.TrimSpace(getArgsRaw(ev, m.k))

	var data string

	// Try reply message first.
	if ev.ReplyToMsgID != 0 {
		msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
		if err == nil && len(msgs) > 0 && msgs[0] != nil && msgs[0].Message != "" {
			data = msgs[0].Message
		}
	}

	// Try URL if no data from reply.
	if data == "" && args != "" {
		if strings.HasPrefix(args, "http://") || strings.HasPrefix(args, "https://") {
			httpClient := &http.Client{Timeout: 10 * time.Second}
			resp, err := httpClient.Get(args)
			if err != nil {
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					m.sf("iloadalias_fetch_error", "{url}", html.EscapeString(args)))
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					m.sf("iloadalias_fetch_error", "{url}", html.EscapeString(args)))
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					m.sf("iloadalias_fetch_error", "{url}", html.EscapeString(args)))
			}
			data = string(body)
		} else {
			// Treat raw arg as JSON data.
			data = args
		}
	}

	if data == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("iloadalias_usage", "{prefix}", html.EscapeString(m.k.Prefix())))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("iloadalias_invalid_json"))
	}

	// Support {"aliases": {...}} and flat {"alias": "cmd"} formats.
	aliasesRaw := parsed
	if inner, ok := parsed["aliases"]; ok {
		if mm, ok := inner.(map[string]interface{}); ok {
			aliasesRaw = mm
		}
	}

	// Support array format [{"alias": "foo", "command": "bar"}, ...]
	if arr, ok := parsed["aliases"]; ok {
		if list, ok := arr.([]interface{}); ok {
			converted := make(map[string]interface{})
			for _, item := range list {
				if entry, ok := item.(map[string]interface{}); ok {
					if a, ok := entry["alias"].(string); ok {
						if c, ok := entry["command"].(string); ok {
							converted[a] = c
						}
					}
				}
			}
			aliasesRaw = converted
		}
	}

	loaded := 0
	for k, v := range aliasesRaw {
		cmd, ok := v.(string)
		if !ok {
			continue
		}
		m.k.AddAlias(k, cmd)
		loaded++
	}

	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving config: %s", html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.sf("iloadalias_done", "{count}", strconv.Itoa(loaded)))
}

// ---------- .unla ----------

func (m *settingsModule) cmdUnla(ctx context.Context, ev *events.NewMessage) error {
	if len(m.k.Aliases) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("aliases_empty"))
	}

	aliasesExport := make(map[string]string)
	for k, v := range m.k.Aliases {
		aliasesExport[k] = v
	}

	exportData, err := json.MarshalIndent(map[string]interface{}{"aliases": aliasesExport}, "", "  ")
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error serializing aliases: %s", html.EscapeString(err.Error())))
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("unla_uploading")); err != nil {
		return err
	}

	tmpPath := filepath.Join(os.TempDir(), "aliases.json")
	if err := os.WriteFile(tmpPath, exportData, 0600); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error writing temp file: %s", html.EscapeString(err.Error())))
	}
	defer os.Remove(tmpPath)

	caption := m.sf("unla_file_caption", "{prefix}", html.EscapeString(m.k.Prefix()))

	return sendDocument(ctx, m.k, ev.PeerID, tmpPath, caption)
}

// ---------- .lang ----------

func (m *settingsModule) cmdLang(ctx context.Context, ev *events.NewMessage) error {
	args := strings.Fields(getArgsRaw(ev, m.k))

	if len(args) == 0 {
		current := m.k.Config.Language
		if current == "" {
			current = "en"
		}
		// Show current language and available ones.
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf(`<tg-emoji emoji-id="5397575638146110953">🌎</tg-emoji> <b>Language:</b> <code>%s</code>`+"\nAvailable: %s",
				html.EscapeString(current),
				strings.Join(settingsAvailableLangs, ", ")))
	}

	newLang := strings.ToLower(args[0])
	validLang := false
	for _, l := range settingsAvailableLangs {
		if l == newLang {
			validLang = true
			break
		}
	}
	if !validLang {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("lang_available", "{langs}", strings.Join(settingsAvailableLangs, ", ")))
	}

	m.k.Config.Language = newLang
	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving config: %s", html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.sf("lang_changed", "{lang}", html.EscapeString(newLang)))
}

// ---------- .cleardb ----------

func (m *settingsModule) cmdClearDB(ctx context.Context, ev *events.NewMessage) error {
	args := strings.Fields(getArgsRaw(ev, m.k))
	if !hasFlag(args, "--yes") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("cleardb_confirm"))
	}

	dbPath := "mcub.db"

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("cleardb_missing", "{path}", html.EscapeString(dbPath)))
	}

	if err := os.Remove(dbPath); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("cleardb_error", "{error}", html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.sf("cleardb_done", "{path}", html.EscapeString(dbPath)))
}

// ---------- .clearmodules ----------

func (m *settingsModule) cmdClearModules(ctx context.Context, ev *events.NewMessage) error {
	args := strings.Fields(getArgsRaw(ev, m.k))
	modulesDir := m.k.ModulesLoadedDir
	if modulesDir == "" {
		modulesDir = "modules_loaded"
	}

	if !hasFlag(args, "--yes") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("clearmodules_confirm", "{path}", html.EscapeString(modulesDir)))
	}

	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("clearmodules_missing", "{path}", html.EscapeString(modulesDir)))
	}

	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("clearmodules_error", "{error}", html.EscapeString(err.Error())))
	}

	deleted := 0
	for _, entry := range entries {
		target := filepath.Join(modulesDir, entry.Name())
		var removeErr error
		if entry.IsDir() {
			removeErr = os.RemoveAll(target)
		} else {
			removeErr = os.Remove(target)
		}
		if removeErr == nil {
			deleted++
		}
	}

	if deleted == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("clearmodules_missing", "{path}", html.EscapeString(modulesDir)))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		m.sf("clearmodules_done", "{count}", strconv.Itoa(deleted)))
}

// ---------- .clearcache ----------

func (m *settingsModule) cmdClearCache(ctx context.Context, ev *events.NewMessage) error {
	args := strings.Fields(getArgsRaw(ev, m.k))
	if !hasFlag(args, "--yes") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("clearcache_confirm"))
	}

	if m.k.Cache != nil {
		m.k.Cache.Clear()
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("clearcache_done"))
}

// ---------- .mcubinfo ----------

func (m *settingsModule) cmdMcubInfo(ctx context.Context, ev *events.NewMessage) error {
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("mcubinfo_html"))
}

// ---------- .piped ----------

func (m *settingsModule) cmdPiped(ctx context.Context, ev *events.NewMessage) error {
	raw := strings.TrimSpace(getArgsRaw(ev, m.k))

	if raw == "" {
		// Toggle current state.
		current := m.k.Config.Piped
		m.k.Config.Piped = !current
		if err := m.saveConfig(); err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("❌ Error saving config: %s", html.EscapeString(err.Error())))
		}
		if m.k.Config.Piped {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("piped_on"))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("piped_off"))
	}

	switch strings.ToLower(raw) {
	case "on", "1", "true":
		m.k.Config.Piped = true
	case "off", "0", "false":
		m.k.Config.Piped = false
	default:
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			m.sf("piped_usage", "{prefix}", html.EscapeString(m.k.Prefix())))
	}

	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving config: %s", html.EscapeString(err.Error())))
	}

	if m.k.Config.Piped {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("piped_on"))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.s("piped_off"))
}

// ---------- .mcub ----------

func (m *settingsModule) cmdMcub(ctx context.Context, ev *events.NewMessage) error {
	version := m.k.Version

	text := fmt.Sprintf(
		`<tg-emoji emoji-id="5469945764069280010">🔮</tg-emoji>`+
			`<tg-emoji emoji-id="5469943045354984820">🔮</tg-emoji>`+
			`<tg-emoji emoji-id="5469879466954098867">🔮</tg-emoji>`+
			` <b>MCUB-Go</b> <code>%s</code>`,
		html.EscapeString(version),
	)

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, text)
}
