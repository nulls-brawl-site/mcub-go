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
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji IDs for the settings module (matching Python source).
const (
	emojiSettingsGear    = `<tg-emoji emoji-id="5271785531192491349">⚙️</tg-emoji>`
	emojiSettingsCheck   = `<tg-emoji emoji-id="5454096630372379732">☑️</tg-emoji>`
	emojiSettingsError   = `<tg-emoji emoji-id="5388785832956016892">❌</tg-emoji>`
	emojiSettingsAlias   = `<tg-emoji emoji-id="5334673106202010226">✏️</tg-emoji>`
	emojiSettingsLang    = `<tg-emoji emoji-id="5397575638146110953">🌎</tg-emoji>`
	emojiSettingsDanger  = `<tg-emoji emoji-id="5904692292324692386">⚠️</tg-emoji>`
	emojiSettingsInfo    = `<tg-emoji emoji-id="5440539497383087970">ℹ️</tg-emoji>`
	emojiSettingsMcub1   = `<tg-emoji emoji-id="5469945764069280010">🔮</tg-emoji>`
	emojiSettingsMcub2   = `<tg-emoji emoji-id="5469943045354984820">🔮</tg-emoji>`
	emojiSettingsMcub3   = `<tg-emoji emoji-id="5469879466954098867">🔮</tg-emoji>`
	emojiSettingsBranch  = `<tg-emoji emoji-id="5449918202718985124">🌳</tg-emoji>`
	emojiSettingsTelethon = `<tg-emoji emoji-id="5397575638146110953">🌎</tg-emoji>`
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
			fmt.Sprintf("%s <b>Usage:</b> <code>%ssetprefix [prefix] [id/@username/reply]</code>\n"+
				"<b>Current prefix:</b> <code>%s</code>",
				emojiSettingsGear, html.EscapeString(prefix), html.EscapeString(prefix)))
	}

	newPrefix := args[0]
	if len([]rune(newPrefix)) != 1 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			emojiSettingsError+" Prefix must be exactly one character.")
	}

	var targetID int64
	if len(args) > 1 {
		id, err := m.resolvePrefixTarget(ctx, ev, args[1])
		if err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("%s Could not resolve target: %s", emojiSettingsError, html.EscapeString(err.Error())))
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
			fmt.Sprintf("%s Error saving config: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Prefix for <code>%d</code>: <code>%s</code> → <code>%s</code>",
			emojiSettingsCheck, targetID,
			html.EscapeString(oldPrefix), html.EscapeString(newPrefix)))
}

// ---------- .addalias ----------

func (m *settingsModule) cmdAddAlias(ctx context.Context, ev *events.NewMessage) error {
	raw := getArgsRaw(ev, m.k)
	prefix := m.k.Prefix()

	if !strings.Contains(raw, "=") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Usage:</b> <code>%saddalias alias=command</code>",
				emojiSettingsAlias, html.EscapeString(prefix)))
	}

	parts := strings.SplitN(raw, "=", 2)
	alias := strings.TrimSpace(parts[0])
	command := strings.TrimSpace(parts[1])

	if alias == "" || command == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Usage:</b> <code>%saddalias alias=command</code>",
				emojiSettingsAlias, html.EscapeString(prefix)))
	}

	// Check command exists.
	cmdBase := strings.Fields(command)[0]
	if _, ok := m.k.CommandHandlers[cmdBase]; !ok {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Command <code>%s</code> not found.",
				emojiSettingsError, html.EscapeString(cmdBase)))
	}

	m.k.AddAlias(alias, command)
	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error saving config: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Alias created: <code>%s%s</code> → <code>%s%s</code>",
			emojiSettingsCheck,
			html.EscapeString(prefix), html.EscapeString(alias),
			html.EscapeString(prefix), html.EscapeString(command)))
}

// ---------- .delalias ----------

func (m *settingsModule) cmdDelAlias(ctx context.Context, ev *events.NewMessage) error {
	args := strings.TrimSpace(getArgsRaw(ev, m.k))
	prefix := m.k.Prefix()

	if args == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Usage:</b> <code>%sdelalias alias</code>",
				emojiSettingsAlias, html.EscapeString(prefix)))
	}

	if _, ok := m.k.Aliases[args]; !ok {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Alias <code>%s</code> not found.",
				emojiSettingsError, html.EscapeString(args)))
	}

	m.k.RemoveAlias(args)
	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error saving config: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Alias <code>%s%s</code> removed.",
			emojiSettingsCheck, html.EscapeString(prefix), html.EscapeString(args)))
}

// ---------- .aliases ----------

func (m *settingsModule) cmdAliases(ctx context.Context, ev *events.NewMessage) error {
	if len(m.k.Aliases) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			emojiSettingsAlias+" No aliases configured.")
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
					fmt.Sprintf("%s Failed to fetch URL: %s", emojiSettingsError, html.EscapeString(err.Error())))
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					fmt.Sprintf("%s HTTP error: %d", emojiSettingsError, resp.StatusCode))
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					fmt.Sprintf("%s Error reading response: %s", emojiSettingsError, html.EscapeString(err.Error())))
			}
			data = string(body)
		} else {
			// Treat raw arg as JSON data.
			data = args
		}
	}

	if data == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Usage:</b> <code>%siloadalias [url]</code> or reply to a message containing JSON aliases.",
				emojiSettingsAlias, html.EscapeString(m.k.Prefix())))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			emojiSettingsError+" Invalid JSON format.")
	}

	// Support {"aliases": {...}} and flat {"alias": "cmd"} formats.
	aliasesRaw := parsed
	if inner, ok := parsed["aliases"]; ok {
		if m, ok := inner.(map[string]interface{}); ok {
			aliasesRaw = m
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
			fmt.Sprintf("%s Error saving config: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Imported <b>%d</b> aliases.", emojiSettingsCheck, loaded))
}

// ---------- .unla ----------

func (m *settingsModule) cmdUnla(ctx context.Context, ev *events.NewMessage) error {
	if len(m.k.Aliases) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			emojiSettingsAlias+" No aliases to export.")
	}

	aliasesExport := make(map[string]string)
	for k, v := range m.k.Aliases {
		aliasesExport[k] = v
	}

	exportData, err := json.MarshalIndent(map[string]interface{}{"aliases": aliasesExport}, "", "  ")
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error serializing aliases: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, emojiSettingsAlias+" Uploading aliases..."); err != nil {
		return err
	}

	tmpPath := filepath.Join(os.TempDir(), "aliases.json")
	if err := os.WriteFile(tmpPath, exportData, 0600); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error writing temp file: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}
	defer os.Remove(tmpPath)

	caption := fmt.Sprintf("%s <b>Aliases export</b>\nImport with: <code>%siloadalias [url]</code>",
		emojiSettingsAlias, html.EscapeString(m.k.Prefix()))

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
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Current language:</b> <code>%s</code>\n<b>Available:</b> <code>%s</code>",
				emojiSettingsLang, html.EscapeString(current),
				strings.Join(settingsAvailableLangs, " | ")))
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
			fmt.Sprintf("%s Invalid language. Available: <code>%s</code>",
				emojiSettingsError, strings.Join(settingsAvailableLangs, " | ")))
	}

	m.k.Config.Language = newLang
	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error saving config: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Language changed to <code>%s</code>.", emojiSettingsLang, html.EscapeString(newLang)))
}

// ---------- .cleardb ----------

func (m *settingsModule) cmdClearDB(ctx context.Context, ev *events.NewMessage) error {
	args := strings.Fields(getArgsRaw(ev, m.k))
	if !hasFlag(args, "--yes") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>This will delete the database file!</b>\n"+
				"Run <code>%scleardb --yes</code> to confirm.",
				emojiSettingsDanger, html.EscapeString(m.k.Prefix())))
	}

	dbPath := "mcub.db"
	if m.k.DB != nil {
		// Try to get DB file path from kernel.
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Database file <code>%s</code> not found.",
				emojiSettingsError, html.EscapeString(dbPath)))
	}

	if err := os.Remove(dbPath); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error deleting database: %s",
				emojiSettingsError, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Database file <code>%s</code> deleted.", emojiSettingsCheck, html.EscapeString(dbPath)))
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
			fmt.Sprintf("%s <b>This will delete all user modules in</b> <code>%s</code>!\n"+
				"Run <code>%sclearmodules --yes</code> to confirm.",
				emojiSettingsDanger, html.EscapeString(modulesDir),
				html.EscapeString(m.k.Prefix())))
	}

	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Modules directory <code>%s</code> not found.",
				emojiSettingsError, html.EscapeString(modulesDir)))
	}

	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error reading directory: %s",
				emojiSettingsError, html.EscapeString(err.Error())))
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
			fmt.Sprintf("%s No modules found in <code>%s</code>.",
				emojiSettingsInfo, html.EscapeString(modulesDir)))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Deleted <b>%d</b> module(s) from <code>%s</code>.",
			emojiSettingsCheck, deleted, html.EscapeString(modulesDir)))
}

// ---------- .clearcache ----------

func (m *settingsModule) cmdClearCache(ctx context.Context, ev *events.NewMessage) error {
	args := strings.Fields(getArgsRaw(ev, m.k))
	if !hasFlag(args, "--yes") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>This will clear the kernel cache!</b>\n"+
				"Run <code>%sclearcache --yes</code> to confirm.",
				emojiSettingsDanger, html.EscapeString(m.k.Prefix())))
	}

	if m.k.Cache != nil {
		m.k.Cache.Clear()
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		emojiSettingsCheck+" Kernel cache cleared.")
}

// ---------- .mcubinfo ----------

func (m *settingsModule) cmdMcubInfo(ctx context.Context, ev *events.NewMessage) error {
	info := `<blockquote><b>What is a userbot?</b>

A <b>userbot</b> is a program that works under a regular Telegram account (not a bot account). Unlike official bots (@BotFather), userbots can:

• Read and send messages in any chat
• Automate routine tasks
• Extend Telegram with custom commands
• Work invisibly to other users</blockquote>

<blockquote><b>MCUB</b> — Mitrich Core UserBot

An open-source, modular userbot framework built for power users. Supports Python modules, pipelines, aliases, multi-account, and much more.</blockquote>`

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, info)
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
				fmt.Sprintf("%s Error saving config: %s", emojiSettingsError, html.EscapeString(err.Error())))
		}
		if m.k.Config.Piped {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				emojiSettingsCheck+" Pipeline mode <b>enabled</b>.")
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			emojiSettingsCheck+" Pipeline mode <b>disabled</b>.")
	}

	switch strings.ToLower(raw) {
	case "on", "1", "true":
		m.k.Config.Piped = true
	case "off", "0", "false":
		m.k.Config.Piped = false
	default:
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Usage: <code>%spiped [on/off]</code>",
				emojiSettingsError, html.EscapeString(m.k.Prefix())))
	}

	if err := m.saveConfig(); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Error saving config: %s", emojiSettingsError, html.EscapeString(err.Error())))
	}

	state := "disabled"
	if m.k.Config.Piped {
		state = "enabled"
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Pipeline mode <b>%s</b>.", emojiSettingsCheck, state))
}

// ---------- .mcub ----------

func (m *settingsModule) cmdMcub(ctx context.Context, ev *events.NewMessage) error {
	version := m.k.Version

	text := fmt.Sprintf(
		"<blockquote>%s%s%s <code>%s</code></blockquote>\n\n"+
			"<blockquote>%s <strong>MCUB-Go</strong>\n"+
			"%s Version <code>%s</code></blockquote>",
		emojiSettingsMcub1, emojiSettingsMcub2, emojiSettingsMcub3,
		html.EscapeString(version),
		emojiSettingsTelethon,
		emojiSettingsBranch,
		html.EscapeString(version),
	)

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, text)
}
