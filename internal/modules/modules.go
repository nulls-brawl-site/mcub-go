// Package modules – modules.go
//
// loaderModule is the Go port of loader.py (Loader class, v1.1.5).
// It provides module management commands: .iload, .dlm, .um, .unlm, .reload,
// .addrepo, .delrepo.
//
// SPDX-License-Identifier: MIT
// author: @Hairpin00 (Python original), Go port by mcub-go contributors
package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/exceptions"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/gotd/td/tg"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// ─────────────────────────────────────────────────────────────────────────────
// Custom emoji – identical to CUSTOM_EMOJI dict in loader.py
// ─────────────────────────────────────────────────────────────────────────────

const (
	ceLoading      = `<tg-emoji emoji-id="5893368370530621889">🔜</tg-emoji>`
	ceDependencies = `<tg-emoji emoji-id="5328311576736833844">🟠</tg-emoji>`
	ceConfused     = `<tg-emoji emoji-id="5249119354825487565">🫨</tg-emoji>`
	ceError        = `<tg-emoji emoji-id="5370843963559254781">😖</tg-emoji>`
	ceFile         = `<tg-emoji emoji-id="5269353173390225894">💾</tg-emoji>`
	ceProcess      = `<tg-emoji emoji-id="5426958067763804056">⏳</tg-emoji>`
	ceBlocked      = `<tg-emoji emoji-id="5431895003821513760">🚫</tg-emoji>`
	ceWarning      = `<tg-emoji emoji-id="5409235172979672859">⚠️</tg-emoji>`
	ceStone        = `<tg-emoji emoji-id="4904687665158292410">🗿</tg-emoji>`
	ceIdea         = `<tg-emoji emoji-id="5411134407517964108">💡</tg-emoji>`
	ceSuccess      = `<tg-emoji emoji-id="5118861066981344121">✅</tg-emoji>`
	ceTest         = `<tg-emoji emoji-id="5134183530313548836">🧪</tg-emoji>`
	ceCrystal      = `<tg-emoji emoji-id="5368585403467048206">🪬</tg-emoji>`
	ceSparkle      = `<tg-emoji emoji-id="5426900601101374618">🪩</tg-emoji>`
	ceFolder       = `<tg-emoji emoji-id="5217444336089714383">📂</tg-emoji>`
	ceUpload       = `<tg-emoji emoji-id="5253526631221307799">📤</tg-emoji>`
	ceShield       = `<tg-emoji emoji-id="5253671358734281000">🛡</tg-emoji>`
	ceAngel        = `<tg-emoji emoji-id="5404521025465518254">😇</tg-emoji>`
	ceNerd         = `<tg-emoji emoji-id="5465154440287757794">🤓</tg-emoji>`
	ceCloud        = `<tg-emoji emoji-id="5370947515220761242">🌩</tg-emoji>`
	ceReload       = `<tg-emoji emoji-id="5893368370530621889">🔜</tg-emoji>`
	ceConvert      = `<tg-emoji emoji-id="5332600281970517875">🔄</tg-emoji>`
	ceDownload     = `<tg-emoji emoji-id="5469785308386041323">⬇️</tg-emoji>`
	ceNoCmd        = `<tg-emoji emoji-id="5429428837895141860">🫨</tg-emoji>`
	ceAuthor       = `<tg-emoji emoji-id="5332630862137685609">💖</tg-emoji>`
	ceLib          = `<tg-emoji emoji-id="5359785904535774578">💼</tg-emoji>`
	ceWait         = `<tg-emoji emoji-id="5326015457155620929">🧳</tg-emoji>`
	ceLink         = `<tg-emoji emoji-id="5411527152212411235">🔗</tg-emoji>`
	ceInlineBot    = `<tg-emoji emoji-id="5372981976804366741">🤖</tg-emoji>`
)

// randomEmojis mirrors the RANDOM_EMOJIS list in loader.py.
var randomEmojis = []string{
	"ಠ_ಠ",
	"( ཀ ʖ̯ ཀ)",
	"(◕‿◕✿)",
	"(つ･･)つ",
	"༼つ◕_◕༽つ",
	"(•_•)",
	"☜(ﾟヮﾟ☜)",
	"(☞ﾟヮﾟ)☞",
	"ʕ•ᴥ•ʔ",
	"(づ￣ ³￣)づ",
}

func pickRandomEmoji() string {
	return randomEmojis[rand.Intn(len(randomEmojis))]
}

// ─────────────────────────────────────────────────────────────────────────────
// DB / config keys
// ─────────────────────────────────────────────────────────────────────────────

const (
	dbKeyLoaderRepos = "repos"

	defaultRepo = "https://raw.githubusercontent.com/hairpin01/MCUB-fork/main/modules"
)

// loaderCfg mirrors the ModuleConfig defined in loader.py.
type loaderCfg struct {
	ProtectSystem bool // loader_protect_system (default true)
	ShowBanners   bool // loader_show_banners   (default true)
	AllowHikka    bool // loader_allow_hikka_modules (default true)
}

// ─────────────────────────────────────────────────────────────────────────────
// loaderModule
// ─────────────────────────────────────────────────────────────────────────────

// richSourceInfo mirrors Python's _module_sources dict entries.
// It is stored locally in the loaderModule (not in Kernel.ModuleSources which
// is a simple string-typed enum).
type richSourceInfo struct {
	URL          string
	Repo         string
	OriginalName string
}

// loaderModule provides module-management commands ported from loader.py.
type loaderModule struct {
	k          *kernel.Kernel
	repoMgr    *loader.RepositoryManager
	cfg        loaderCfg
	srcInfoMap map[string]richSourceInfo // module name -> source info
}

func newLoaderModule() loader.Module { return &loaderModule{} }

// Name implements loader.Module.
func (m *loaderModule) Name() string { return "loader" }

// OnLoad implements loader.Module.
func (m *loaderModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("loader: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	m.repoMgr = loader.NewRepositoryManager()
	m.srcInfoMap = make(map[string]richSourceInfo)

	// Load config (defaults: all true).
	m.cfg = loaderCfg{ProtectSystem: true, ShowBanners: true, AllowHikka: true}
	if kern.Config != nil {
		if cfgMap, err := kern.Config.GetModuleConfig("loader", map[string]interface{}{
			"loader_protect_system":     true,
			"loader_show_banners":       true,
			"loader_allow_hikka_modules": true,
		}); err == nil {
			if v, ok2 := cfgMap["loader_protect_system"]; ok2 {
				m.cfg.ProtectSystem = toBool(v, true)
			}
			if v, ok2 := cfgMap["loader_show_banners"]; ok2 {
				m.cfg.ShowBanners = toBool(v, true)
			}
			if v, ok2 := cfgMap["loader_allow_hikka_modules"]; ok2 {
				m.cfg.AllowHikka = toBool(v, true)
			}
		}
	}

	// Restore persisted user repos.
	if kern.DB != nil {
		if saved, ok2, err := kern.DB.ModuleGet("loader", dbKeyLoaderRepos); err == nil && ok2 && saved != "" {
			var urls []string
			if jsonErr := json.Unmarshal([]byte(saved), &urls); jsonErr == nil {
				for _, u := range urls {
					_ = m.repoMgr.AddRepoURL(u)
				}
			}
		}
	}

	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *loaderModule) OnUnload(k interface{}) error {
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
// Descriptions match the doc_en strings in loader.py @command decorators.
func (m *loaderModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "iload",
			Description: "<reply> load module from reply",
			Handler:     m.cmdIload,
		},
		{
			Name:        "dlm",
			Description: "<URL/[-send] [name]/[-list] [name/None]> download and install module from URL or repo",
			Handler:     m.cmdDlm,
		},
		{
			Name:        "um",
			Description: "<n> unload module by name",
			Handler:     m.cmdUm,
		},
		{
			Name:        "unlm",
			Description: "<n> unload module as file",
			Handler:     m.cmdUnlm,
		},
		{
			Name:        "reload",
			Description: "<name/None> reload module(s)",
			Handler:     m.cmdReload,
		},
		{
			Name:        "addrepo",
			Description: "<URL> add module repository URL",
			Handler:     m.cmdAddrepo,
		},
		{
			Name:        "delrepo",
			Description: "<ID> remove module repository",
			Handler:     m.cmdDelrepo,
		},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Misc helpers
// ─────────────────────────────────────────────────────────────────────────────

func toBool(v interface{}, def bool) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case int:
		return x != 0
	case string:
		low := strings.ToLower(x)
		return low == "true" || low == "1" || low == "yes"
	}
	return def
}

func (m *loaderModule) parseArgs(ev *events.NewMessage) []string {
	body := strings.TrimPrefix(ev.Text(), m.k.Prefix())
	parts := strings.Fields(body)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

func (m *loaderModule) parseArgsRaw(ev *events.NewMessage) string {
	body := strings.TrimPrefix(ev.Text(), m.k.Prefix())
	idx := strings.IndexAny(body, " \t")
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(body[idx+1:])
}

func (m *loaderModule) edit(ctx context.Context, ev *events.NewMessage, text string) error {
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, text)
}

// replyFilename returns the filename of the document attached to the replied message.
func (m *loaderModule) replyFilename(ctx context.Context, ev *events.NewMessage) (string, error) {
	if ev.ReplyToMsgID == 0 {
		return "", nil
	}
	msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
	if err != nil || len(msgs) == 0 || msgs[0] == nil {
		return "", err
	}
	media, ok := msgs[0].Media.(*tg.MessageMediaDocument)
	if !ok || media == nil {
		return "", nil
	}
	doc, ok := media.Document.(*tg.Document)
	if !ok || doc == nil {
		return "", nil
	}
	for _, attr := range doc.Attributes {
		if fa, ok2 := attr.(*tg.DocumentAttributeFilename); ok2 && fa.FileName != "" {
			return fa.FileName, nil
		}
	}
	return fmt.Sprintf("%d.bin", doc.ID), nil
}

// downloadReplyFile downloads the document from the replied message.
func (m *loaderModule) downloadReplyFile(ctx context.Context, ev *events.NewMessage, destPath string) error {
	_, err := m.k.Client.DownloadMedia(ctx, mcubclient.DownloadMediaParams{
		ChatID:    ev.PeerID,
		MessageID: ev.ReplyToMsgID,
		FilePath:  destPath,
	})
	return err
}

// allRepoURLs returns the default repo followed by all user-added repos.
func (m *loaderModule) allRepoURLs() []string {
	repos := []string{defaultRepo}
	for _, r := range m.repoMgr.ListRepos() {
		repos = append(repos, r.URL)
	}
	return repos
}

// isURL returns true when s looks like an HTTP/HTTPS/raw github URL.
func isURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return true
	}
	return strings.HasPrefix(s, "raw.githubusercontent.com")
}

// saveRepos persists the current user-added repos to DB.
func (m *loaderModule) saveRepos() {
	if m.k == nil || m.k.DB == nil {
		return
	}
	repos := m.repoMgr.ListRepos()
	urls := make([]string, 0, len(repos))
	for _, r := range repos {
		urls = append(urls, r.URL)
	}
	data, err := json.Marshal(urls)
	if err == nil {
		_ = m.k.DB.ModuleSet("loader", dbKeyLoaderRepos, string(data))
	}
}

// findModuleFile searches modules_loaded/ for a .py file whose stem matches name.
func (m *loaderModule) findModuleFile(name string) string {
	dir := m.k.ModulesLoadedDir
	if dir == "" {
		dir = "modules_loaded"
	}
	exact := filepath.Join(dir, name+".py")
	if _, err := os.Stat(exact); err == nil {
		return exact
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	lower := strings.ToLower(name)
	for _, e := range entries {
		if !e.IsDir() && strings.EqualFold(strings.TrimSuffix(e.Name(), ".py"), lower) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// findModuleFileAny searches both modules/ and modules_loaded/.
func (m *loaderModule) findModuleFileAny(name string) string {
	if p := m.findModuleFile(name); p != "" {
		return p
	}
	dir := m.k.ModulesDir
	if dir == "" {
		dir = "modules"
	}
	exact := filepath.Join(dir, name+".py")
	if _, err := os.Stat(exact); err == nil {
		return exact
	}
	return ""
}

// loadedModuleNames returns a sorted list of user-loaded module names.
func (m *loaderModule) loadedModuleNames() []string {
	names := make([]string, 0, len(m.k.LoadedModules))
	for n := range m.k.LoadedModules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// findModuleCaseInsensitive finds a module by case-insensitive name.
// Returns the actual name and whether it was found.
func (m *loaderModule) findModuleCaseInsensitive(name string) (string, bool) {
	lower := strings.ToLower(name)
	for n := range m.k.LoadedModules {
		if strings.ToLower(n) == lower {
			return n, true
		}
	}
	for n := range m.k.SystemModules {
		if strings.ToLower(n) == lower {
			return n, true
		}
	}
	return "", false
}

// getSourceLink returns a blockquote HTML link for the module's source,
// mirroring _get_source_link() in loader.py.
func (m *loaderModule) getSourceLink(moduleName string) string {
	src, ok := m.srcInfoMap[moduleName]
	if !ok {
		return ""
	}
	if src.URL != "" {
		return fmt.Sprintf(
			`<blockquote>%s Source link %s</blockquote>`,
			ceLink, src.URL,
		)
	}
	if src.Repo != "" {
		repo := strings.TrimRight(src.Repo, "/")
		origName := src.OriginalName
		if origName == "" {
			origName = moduleName
		}
		return fmt.Sprintf(
			`<blockquote>%s Source link %s/%s.py</blockquote>`,
			ceLink, repo, origName,
		)
	}
	return ""
}

// getInlineBotUsername returns the inline bot @username if configured.
func (m *loaderModule) getInlineBotUsername() string {
	if m.k == nil || m.k.Config == nil {
		return ""
	}
	if m.k.Config.InlineBotUsername != nil && *m.k.Config.InlineBotUsername != "" {
		return strings.TrimLeft(*m.k.Config.InlineBotUsername, "@")
	}
	return ""
}

// moduleDir returns modules_loaded dir or default.
func (m *loaderModule) moduleDir() string {
	if m.k.ModulesLoadedDir != "" {
		return m.k.ModulesLoadedDir
	}
	return "modules_loaded"
}

// addLog helper type for install logging (mirrors local add_log in Python).
type installLog struct {
	entries []string
}

func (il *installLog) add(msg string) {
	ts := time.Now().Format("15:04:05")
	il.entries = append(il.entries, fmt.Sprintf("[%s] %s", ts, msg))
}

func (il *installLog) text() string {
	return strings.Join(il.entries, "\n")
}

// ─────────────────────────────────────────────────────────────────────────────
// Metadata helpers
// ─────────────────────────────────────────────────────────────────────────────

type moduleMetadata struct {
	Version    string
	Author     string
	Desc       string
	BannerURL  string
	ClassName  string
	Commands   map[string]string
}

func parseMetadata(code string) moduleMetadata {
	md := moduleMetadata{
		Version:  "1.0.0",
		Author:   "unknown",
		Commands: map[string]string{},
	}
	// Use kernel.GetModuleMetadata if available, else parse inline.
	for _, line := range strings.Split(code, "\n") {
		stripped := strings.TrimSpace(line)
		if !strings.HasPrefix(stripped, "#") {
			continue
		}
		content := strings.TrimSpace(strings.TrimPrefix(stripped, "#"))
		switch {
		case strings.HasPrefix(content, "version:"):
			md.Version = strings.TrimSpace(strings.TrimPrefix(content, "version:"))
		case strings.HasPrefix(content, "author:"):
			md.Author = strings.TrimSpace(strings.TrimPrefix(content, "author:"))
		case strings.HasPrefix(content, "description:"):
			md.Desc = strings.TrimSpace(strings.TrimPrefix(content, "description:"))
		case strings.HasPrefix(content, "banner_url:"):
			md.BannerURL = strings.TrimSpace(strings.TrimPrefix(content, "banner_url:"))
		}
	}
	return md
}

// ─────────────────────────────────────────────────────────────────────────────
// _send_module_loaded equivalent
// ─────────────────────────────────────────────────────────────────────────────

// buildModuleLoadedMsg constructs the "module loaded" HTML message,
// mirroring _send_module_loaded() in loader.py.
// cmdLines: list of command strings (from buildCommandsList).
// sourceLink: from getSourceLink().
// emoji: random emoji from RANDOM_EMOJIS.
func (m *loaderModule) buildModuleLoadedMsg(
	modName, version, author, desc string,
	emoji, cmdLines, sourceLink string,
) string {
	return sf(m.k, "loader", "module_loaded", map[string]interface{}{
		"success":      ceSuccess,
		"module_name":  modName,
		"version":      version,
		"emoji":        emoji,
		"idea":         ceIdea,
		"description":  desc,
		"emoji_author": ceAuthor,
		"author":       author,
		"commands_list": cmdLines,
		"source_link":  sourceLink,
	})
}

// buildCommandsList mirrors _build_commands_list() in loader.py.
// It returns an HTML string listing commands with their descriptions,
// plus any inline commands registered by the module.
func (m *loaderModule) buildCommandsList(modName string, cmds []loader.Command) string {
	if len(cmds) == 0 {
		// Check inline commands too.
		return m.buildInlineCommandsSection(modName)
	}

	var sb strings.Builder
	for _, cmd := range cmds {
		desc := cmd.Description
		if desc == "" {
			desc = sf(m.k, "loader", "no_cmd_desc", map[string]interface{}{"no_cmd": ceNoCmd})
		}
		line := sf(m.k, "loader", "command_line", map[string]interface{}{
			"crystal": ceCrystal,
			"prefix":  m.k.Prefix(),
			"cmd":     cmd.Name,
			"desc":    desc,
		})
		sb.WriteString(line + "\n")
	}

	sb.WriteString(m.buildInlineCommandsSection(modName))
	return sb.String()
}

// buildInlineCommandsSection appends inline commands to the commands list,
// mirroring the inline commands block in _build_commands_list().
func (m *loaderModule) buildInlineCommandsSection(modName string) string {
	inlineCmds := m.k.GetModuleInlineCommands(modName)
	if len(inlineCmds) == 0 {
		return ""
	}
	botUsername := m.getInlineBotUsername()
	if botUsername == "" {
		botUsername = "bot"
	}
	var sb strings.Builder
	for _, ic := range inlineCmds {
		if ic.Description != "" {
			sb.WriteString(fmt.Sprintf("%s <code>@%s %s</code> - <b>%s</b>\n",
				ceInlineBot, botUsername, ic.Name, ic.Description))
		} else {
			sb.WriteString(fmt.Sprintf("%s <code>@%s %s</code>\n",
				ceInlineBot, botUsername, ic.Name))
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Download helper
// ─────────────────────────────────────────────────────────────────────────────

// downloadFromRepos tries each repo in order and returns the source code and
// the repo URL it was found at.
func (m *loaderModule) downloadFromRepos(ctx context.Context, name string) (code, repoURL string, err error) {
	repos := m.allRepoURLs()
	var lastErr error
	for _, r := range repos {
		src, dlErr := m.repoMgr.DownloadModule(ctx, r, name)
		if dlErr == nil {
			return src, r, nil
		}
		lastErr = dlErr
	}
	if lastErr != nil {
		return "", "", lastErr
	}
	return "", "", fmt.Errorf("module %q not found in any repo", name)
}

// findRepoMatches checks which repos contain a module with the given name.
// Returns a list of (repoIndex, repoURL) pairs.
func (m *loaderModule) findRepoMatches(ctx context.Context, moduleName string) []repoMatch {
	repos := m.allRepoURLs()
	normalized := strings.ToLower(moduleName)
	var matches []repoMatch
	for i, repo := range repos {
		mods, err := m.repoMgr.GetModuleList(ctx, repo)
		if err != nil {
			continue
		}
		for _, mod := range mods {
			if strings.ToLower(mod) == normalized {
				matches = append(matches, repoMatch{Index: i, URL: repo})
				break
			}
		}
	}
	return matches
}

type repoMatch struct {
	Index int
	URL   string
}

// ─────────────────────────────────────────────────────────────────────────────
// _run_dlm_install – port of loader.py _run_dlm_install()
// ─────────────────────────────────────────────────────────────────────────────

func (m *loaderModule) runDlmInstall(
	ctx context.Context,
	ev *events.NewMessage,
	moduleOrURL string,
	sendMode bool,
	repoIndex *int,
) error {
	// Determine module name.
	isURLFlag := isURL(moduleOrURL)
	var moduleName string
	if isURLFlag {
		base := filepath.Base(moduleOrURL)
		base = strings.SplitN(base, "?", 2)[0]
		if strings.HasSuffix(strings.ToLower(base), ".py") {
			moduleName = base[:len(base)-3]
		} else {
			if dot := strings.LastIndex(base, "."); dot >= 0 {
				moduleName = base[:dot]
			} else {
				moduleName = base
			}
		}
	} else {
		moduleName = moduleOrURL
	}

	// System module protection.
	if m.cfg.ProtectSystem {
		if _, isSys := m.k.SystemModules[moduleName]; isSys {
			return m.edit(ctx, ev, sf(m.k, "loader", "system_module_install_attempt", map[string]interface{}{
				"confused":    ceConfused,
				"module_name": moduleName,
				"blocked":     ceBlocked,
			}))
		}
	}

	// Check if this is an update.
	_, isUpdate := m.k.LoadedModules[moduleName]
	if !isUpdate {
		_, isUpdate = m.k.SystemModules[moduleName]
	}

	var oldVersion string
	if isUpdate {
		oldFile := m.findModuleFileAny(moduleName)
		if oldFile != "" {
			if src, err := os.ReadFile(oldFile); err == nil { // #nosec G304
				oldMeta := parseMetadata(string(src))
				oldVersion = oldMeta.Version
			}
		}
	}

	il := &installLog{}

	il.add(sf(m.k, "loader", "log_start", map[string]interface{}{
		"action":      ifStr(sendMode, "скачивание", "установку"),
		"module_name": moduleName,
	}))
	il.add(sf(m.k, "loader", "log_mode", map[string]interface{}{
		"mode": ifStr(sendMode, "отправка", "установка"),
	}))
	il.add(sf(m.k, "loader", "log_type", map[string]interface{}{
		"type": ifStr(isURLFlag, "URL", "из репозитория"),
	}))

	// Download code.
	var code string
	var repoURL string

	if isURLFlag {
		il.add(sf(m.k, "loader", "log_download_url", map[string]interface{}{"url": moduleOrURL}))
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, moduleOrURL, nil)
		if err != nil {
			return m.edit(ctx, ev, sf(m.k, "loader", "url_exception", map[string]interface{}{
				"warning": ceWarning,
				"error":   err.Error(),
			}))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			il.add(sf(m.k, "loader", "log_download_exception", map[string]interface{}{"error": err.Error()}))
			return m.edit(ctx, ev, sf(m.k, "loader", "url_exception", map[string]interface{}{
				"warning": ceWarning,
				"error":   err.Error(),
			}))
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			il.add(sf(m.k, "loader", "log_download_failed", map[string]interface{}{"status": resp.StatusCode}))
			return m.edit(ctx, ev, sf(m.k, "loader", "url_download_error", map[string]interface{}{
				"warning": ceWarning,
				"status":  resp.StatusCode,
			}))
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if err != nil {
			return m.edit(ctx, ev, sf(m.k, "loader", "url_exception", map[string]interface{}{
				"warning": ceWarning,
				"error":   err.Error(),
			}))
		}
		code = string(body)
		il.add(sf(m.k, "loader", "log_download_success", map[string]interface{}{"status": resp.StatusCode}))
	} else {
		// Repo download.
		repos := m.allRepoURLs()
		il.add(sf(m.k, "loader", "log_checking_repos", map[string]interface{}{"count": len(repos)}))

		if repoIndex != nil && *repoIndex >= 0 && *repoIndex < len(repos) {
			targetRepo := repos[*repoIndex]
			il.add(sf(m.k, "loader", "log_using_repo", map[string]interface{}{"repo": targetRepo}))
			src, err := m.repoMgr.DownloadModule(ctx, targetRepo, moduleName)
			if err == nil {
				code = src
				repoURL = targetRepo
				il.add(s(m.k, "loader", "log_found_in_repo"))
			} else {
				il.add(s(m.k, "loader", "log_not_found_in_repo"))
			}
		} else {
			for i, repo := range repos {
				il.add(sf(m.k, "loader", "log_checking_repo", map[string]interface{}{
					"index": i + 1,
					"repo":  repo,
				}))
				src, err := m.repoMgr.DownloadModule(ctx, repo, moduleName)
				if err == nil {
					code = src
					repoURL = repo
					il.add(s(m.k, "loader", "log_found_in_repo"))
					break
				}
				il.add(s(m.k, "loader", "log_not_found_in_repo"))
			}
		}
	}

	if code == "" {
		return m.edit(ctx, ev, sf(m.k, "loader", "module_not_found_repos", map[string]interface{}{
			"warning":     ceWarning,
			"module_name": moduleName,
		}))
	}

	// Parse metadata.
	il.add(s(m.k, "loader", "log_getting_metadata"))
	md := parseMetadata(code)
	il.add(sf(m.k, "loader", "log_author", map[string]interface{}{"author": md.Author}))
	il.add(sf(m.k, "loader", "log_version", map[string]interface{}{"version": md.Version}))
	il.add(sf(m.k, "loader", "log_description", map[string]interface{}{"description": md.Desc}))

	// Build action string.
	var action string
	if sendMode {
		action = sf(m.k, "loader", "downloading_module", map[string]interface{}{"download": ceDownload})
	} else if isUpdate {
		if oldVersion != "" && oldVersion != md.Version {
			action = sf(m.k, "loader", "updating_version", map[string]interface{}{
				"reload":      ceLoading,
				"old_version": oldVersion,
				"new_version": md.Version,
			})
		} else {
			action = sf(m.k, "loader", "updating", map[string]interface{}{"reload": ceLoading})
		}
	} else {
		action = sf(m.k, "loader", "installing", map[string]interface{}{"test": ceLoading})
	}

	// Update progress message.
	_ = m.edit(ctx, ev, sf(m.k, "loader", "starting_install", map[string]interface{}{
		"action":      action,
		"module_name": moduleName,
	}))

	destDir := m.moduleDir()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return m.edit(ctx, ev, ceError+" Cannot create modules directory: "+err.Error())
	}
	filePath := filepath.Join(destDir, moduleName+".py")

	// Send mode: save file and send to chat.
	if sendMode {
		il.add(s(m.k, "loader", "log_saving_for_send"))
		if err := os.WriteFile(filePath, []byte(code), 0o644); err != nil {
			return m.edit(ctx, ev, ceError+" "+err.Error())
		}
		defer os.Remove(filePath)

		_ = m.edit(ctx, ev, sf(m.k, "loader", "sending_module", map[string]interface{}{
			"upload":      ceUpload,
			"module_name": moduleName,
		}))

		size, _ := fileSize(filePath)
		captionTpl := sf(m.k, "loader", "file_sent_caption", map[string]interface{}{
			"file":        ceFile,
			"module_name": moduleName,
			"idea":        ceIdea,
			"description": md.Desc,
			"crystal":     ceCrystal,
			"version":     md.Version,
			"angel":       ceAngel,
			"author":      md.Author,
			"folder":      ceFolder,
			"size":        size,
		})
		if _, err := m.k.Client.SendDocument(ctx, ev.PeerID, filePath, captionTpl); err != nil {
			return m.edit(ctx, ev, ceError+" Send failed: "+err.Error())
		}
		il.add(s(m.k, "loader", "log_file_sent"))
		return m.edit(ctx, ev, captionTpl)
	}

	// Install mode.
	il.add(s(m.k, "loader", "log_install_mode"))

	// Hikka module detection.
	if loader.IsHikkaModule(code) {
		il.add(s(m.k, "loader", "log_hikka_detected"))
		if !m.cfg.AllowHikka {
			return m.edit(ctx, ev, sf(m.k, "loader", "hikka_disabled", map[string]interface{}{"warning": ceWarning}))
		}
		// Hikka compat: we don't have a native compat layer in Go yet.
		// Fall through to normal PyLoader installation and let it handle it.
	}

	// Dependencies.
	var deps []string
	if pl := m.k.Loader.PyLoaderInstance(); pl != nil {
		deps = pl.ParseRequires(code)
	}
	if len(deps) > 0 {
		il.add(sf(m.k, "loader", "log_deps_found", map[string]interface{}{"deps": strings.Join(deps, ", ")}))
		var depsEmoji strings.Builder
		for _, dep := range deps {
			depsEmoji.WriteString(ceLib + " " + dep + "\n")
		}
		_ = m.edit(ctx, ev, sf(m.k, "loader", "installing_deps", map[string]interface{}{
			"dependencies": ceDependencies,
			"deps_list":    depsEmoji.String(),
		}))
		if pl := m.k.Loader.PyLoaderInstance(); pl != nil {
			_ = pl.InstallRequires(deps)
		}
	}

	// Handle update: back up old file first.
	var oldFileBackup []byte
	if isUpdate {
		il.add(sf(m.k, "loader", "log_removing_old", map[string]interface{}{"module_name": moduleName}))
		if existing, err := os.ReadFile(filePath); err == nil { // #nosec G304
			oldFileBackup = existing
		}
		// Unregister old commands.
		if mod, ok := m.k.LoadedModules[moduleName]; ok {
			for _, cmd := range mod.Commands() {
				m.k.UnregisterCommand(cmd.Name)
			}
			delete(m.k.LoadedModules, moduleName)
		}
	}

	// Write new file.
	il.add(sf(m.k, "loader", "log_saving_file", map[string]interface{}{"file_path": filePath}))
	if err := os.WriteFile(filePath, []byte(code), 0o644); err != nil {
		return m.edit(ctx, ev, sf(m.k, "loader", "install_failed", map[string]interface{}{
			"blocked": ceBlocked,
			"idea":    ceIdea,
			"log":     html.EscapeString(il.text()),
		}))
	}

	// Load via PyLoader.
	il.add(s(m.k, "loader", "log_loading_to_kernel"))
	var loadErr error
	if m.k.Loader.PyLoaderInstance() != nil {
		loadErr = m.k.Loader.LoadPyFile(filePath)
	} else {
		loadErr = fmt.Errorf("no PyLoader configured")
	}

	if loadErr != nil {
		// Handle CommandConflictError.
		var conflictErr *exceptions.CommandConflictError
		if errors.As(loadErr, &conflictErr) {
			il.add(sf(m.k, "loader", "log_conflict", map[string]interface{}{"error": conflictErr.Error()}))
			if conflictErr.ConflictType == "system" {
				_ = m.edit(ctx, ev, sf(m.k, "loader", "conflict_system_alt", map[string]interface{}{
					"shield":  ceShield,
					"command": conflictErr.Command,
					"log":     html.EscapeString(il.text()),
				}))
			} else {
				_ = m.edit(ctx, ev, sf(m.k, "loader", "conflict_user_alt", map[string]interface{}{
					"error": ceError,
					"log":   html.EscapeString(il.text()),
				}))
			}
		} else {
			il.add(sf(m.k, "loader", "log_install_error", map[string]interface{}{"error": loadErr.Error()}))
			_ = m.edit(ctx, ev, sf(m.k, "loader", "install_failed", map[string]interface{}{
				"blocked": ceBlocked,
				"idea":    ceIdea,
				"log":     html.EscapeString(il.text()),
			}))
		}

		// Restore backup if available.
		if oldFileBackup != nil {
			if writeErr := os.WriteFile(filePath, oldFileBackup, 0o644); writeErr == nil {
				il.add("=> Backup restored: " + filePath)
				// Re-load the old module.
				_ = m.k.Loader.LoadPyFile(filePath)
			}
		} else {
			_ = os.Remove(filePath)
		}
		return nil
	}

	// Success.
	il.add(s(m.k, "loader", "log_module_loaded_kernel"))

	// Track rich source info locally (Python's _module_sources).
	m.srcInfoMap[moduleName] = richSourceInfo{
		URL:          ifStr(isURLFlag, moduleOrURL, ""),
		Repo:         ifStr(!isURLFlag && repoURL != "", repoURL, ""),
		OriginalName: moduleName,
	}
	// Also mark in kernel's simple source registry.
	if m.k.ModuleSources != nil {
		m.k.ModuleSources[moduleName] = loader.ModuleSourceUser
	}

	// Build success message.
	var modCmds []loader.Command
	if mod, ok := m.k.LoadedModules[moduleName]; ok {
		modCmds = mod.Commands()
	}
	cmdList := m.buildCommandsList(moduleName, modCmds)
	emoji := pickRandomEmoji()
	srcLink := m.getSourceLink(moduleName)

	finalMsg := m.buildModuleLoadedMsg(moduleName, md.Version, md.Author, md.Desc, emoji, cmdList, srcLink)

	// Banner support (loader_show_banners).
	if m.cfg.ShowBanners && md.BannerURL != "" &&
		(strings.HasPrefix(md.BannerURL, "http://") || strings.HasPrefix(md.BannerURL, "https://")) {
		// Append banner URL as a link preview fallback.
		// A full InputMediaWebPage is Python-only; in Go we add it as a URL in the message.
		finalMsg += "\n" + md.BannerURL
	}

	return m.edit(ctx, ev, finalMsg)
}

// ─────────────────────────────────────────────────────────────────────────────
// .iload
// ─────────────────────────────────────────────────────────────────────────────

// cmdIload installs a module from the .py (or .zip) file attached to the
// replied message. Mirrors cmd_iload() in loader.py.
func (m *loaderModule) cmdIload(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}
	if ev.ReplyToMsgID == 0 {
		return m.edit(ctx, ev, sf(m.k, "loader", "reply_to_py", map[string]interface{}{"warning": ceWarning}))
	}

	fileName, err := m.replyFilename(ctx, ev)
	if err != nil {
		return m.edit(ctx, ev, sf(m.k, "loader", "reply_to_py", map[string]interface{}{"warning": ceWarning}))
	}
	if fileName == "" {
		return m.edit(ctx, ev, sf(m.k, "loader", "reply_to_py", map[string]interface{}{"warning": ceWarning}))
	}

	ext := strings.ToLower(filepath.Ext(fileName))
	if ext != ".py" && ext != ".zip" && ext != ".tar.gz" && ext != ".tgz" && ext != ".tar" {
		return m.edit(ctx, ev, sf(m.k, "loader", "not_py_file", map[string]interface{}{"warning": ceWarning}))
	}

	_ = m.edit(ctx, ev, sf(m.k, "loader", "wait", map[string]interface{}{"wait": ceWait}))

	destDir := m.moduleDir()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return m.edit(ctx, ev, ceError+" Cannot create modules directory: "+err.Error())
	}

	tmpPath := filepath.Join(destDir, fileName)
	if err := m.downloadReplyFile(ctx, ev, tmpPath); err != nil {
		return m.edit(ctx, ev, ceError+" Download failed: "+err.Error())
	}

	if ext == ".zip" || ext == ".tgz" || ext == ".tar" || ext == ".tar.gz" {
		return m.loadArchive(ctx, ev, tmpPath, destDir)
	}
	return m.loadPyFile(ctx, ev, tmpPath, true /* fromReply */)
}

// loadArchive extracts a zip archive and loads each .py module inside.
func (m *loaderModule) loadArchive(ctx context.Context, ev *events.NewMessage, zipPath, destDir string) error {
	_ = m.edit(ctx, ev, ceLoading+" Extracting archive…")

	result, err := loader.ExtractArchive(zipPath, destDir)
	if err != nil {
		_ = os.Remove(zipPath)
		return m.edit(ctx, ev, ceError+" Extraction failed: <code>"+err.Error()+"</code>")
	}
	_ = os.Remove(zipPath)

	if len(result.Modules) == 0 {
		return m.edit(ctx, ev, ceWarning+" No <code>.py</code> modules found in archive.")
	}

	var loaded []string
	var failed []string
	for _, mod := range result.Modules {
		_ = m.edit(ctx, ev, ceLoading+" Installing <code>"+mod.Name+"</code>…")
		if err2 := m.k.Loader.LoadPyFile(mod.FilePath); err2 != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", mod.Name, err2))
		} else {
			loaded = append(loaded, mod.Name)
		}
	}

	var sb strings.Builder
	if len(loaded) > 0 {
		sb.WriteString(ceSuccess + " Loaded from archive: " + strings.Join(loaded, ", "))
	}
	if len(failed) > 0 {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(ceError + " Failed: " + strings.Join(failed, "; "))
	}
	return m.edit(ctx, ev, sb.String())
}

// loadPyFile loads a single .py file and reports the result.
// fromReply=true means the file came from a Telegram message (iload).
func (m *loaderModule) loadPyFile(ctx context.Context, ev *events.NewMessage, pyPath string, fromReply bool) error {
	baseName := filepath.Base(pyPath)
	stem := strings.TrimSuffix(baseName, ".py")

	// System module protection.
	if m.cfg.ProtectSystem {
		if _, isSys := m.k.SystemModules[stem]; isSys {
			return m.edit(ctx, ev, sf(m.k, "loader", "system_module_update_attempt", map[string]interface{}{
				"confused":    ceConfused,
				"module_name": stem,
				"blocked":     ceBlocked,
			}))
		}
	}

	var il installLog

	// Parse code for hikka/deps.
	if srcBytes, readErr := os.ReadFile(pyPath); readErr == nil { // #nosec G304
		code := string(srcBytes)
		md := parseMetadata(code)

		// Hikka detection.
		if loader.IsHikkaModule(code) {
			il.add(s(m.k, "loader", "log_hikka_detected"))
			if !m.cfg.AllowHikka {
				_ = os.Remove(pyPath)
				return m.edit(ctx, ev, sf(m.k, "loader", "hikka_disabled", map[string]interface{}{"warning": ceWarning}))
			}
		}

		// Deps.
		if pl := m.k.Loader.PyLoaderInstance(); pl != nil {
			if deps := pl.ParseRequires(code); len(deps) > 0 {
				var depsEmoji strings.Builder
				for _, dep := range deps {
					depsEmoji.WriteString(ceLib + " " + dep + "\n")
				}
				_ = m.edit(ctx, ev, sf(m.k, "loader", "installing_deps", map[string]interface{}{
					"dependencies": ceDependencies,
					"deps_list":    depsEmoji.String(),
				}))
				_ = pl.InstallRequires(deps)
			}
		}
		_ = md // suppress unused warning; md is used above
	}

	_ = m.edit(ctx, ev, sf(m.k, "loader", "installing", map[string]interface{}{"test": ceLoading})+
		" <b>"+stem+"</b>…")

	if loadErr := m.k.Loader.LoadPyFile(pyPath); loadErr != nil {
		// Handle CommandConflictError.
		var conflictErr *exceptions.CommandConflictError
		if errors.As(loadErr, &conflictErr) {
			il.add(sf(m.k, "loader", "log_conflict", map[string]interface{}{"error": conflictErr.Error()}))
			if conflictErr.ConflictType == "system" {
				return m.edit(ctx, ev, sf(m.k, "loader", "conflict_system", map[string]interface{}{
					"shield":  ceShield,
					"prefix":  m.k.Prefix(),
					"command": conflictErr.Command,
					"log":     html.EscapeString(il.text()),
				}))
			}
			return m.edit(ctx, ev, sf(m.k, "loader", "conflict_user", map[string]interface{}{
				"error":        ceError,
				"prefix":       m.k.Prefix(),
				"command":      conflictErr.Command,
				"owner_module": conflictErr.Module,
				"log":          html.EscapeString(il.text()),
			}))
		}
		il.add(sf(m.k, "loader", "log_install_error", map[string]interface{}{"error": loadErr.Error()}))
		return m.edit(ctx, ev, sf(m.k, "loader", "install_failed", map[string]interface{}{
			"blocked": ceBlocked,
			"idea":    ceIdea,
			"log":     html.EscapeString(il.text()),
		}))
	}

	// Success: find the loaded module by stem name.
	var modCmds []loader.Command
	modName := stem
	for name, mod := range m.k.LoadedModules {
		if strings.EqualFold(name, stem) ||
			strings.EqualFold(strings.TrimPrefix(name, "userbot-"), stem) {
			modName = name
			modCmds = mod.Commands()
			break
		}
	}

	// Read code again for metadata.
	md := moduleMetadata{Version: "1.0.0", Author: "unknown"}
	if src, err := os.ReadFile(pyPath); err == nil { // #nosec G304
		md = parseMetadata(string(src))
	}

	cmdList := m.buildCommandsList(modName, modCmds)
	emoji := pickRandomEmoji()
	srcLink := m.getSourceLink(modName)
	return m.edit(ctx, ev, m.buildModuleLoadedMsg(modName, md.Version, md.Author, md.Desc, emoji, cmdList, srcLink))
}

// ─────────────────────────────────────────────────────────────────────────────
// .dlm
// ─────────────────────────────────────────────────────────────────────────────

// cmdDlm handles .dlm [-list|-send|-s|--send] [name|URL]
// Mirrors cmd_dlm() in loader.py.
func (m *loaderModule) cmdDlm(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	args := m.parseArgs(ev)

	// No args → show usage (and inline catalog hint if available).
	if len(args) == 0 {
		return m.edit(ctx, ev, sf(m.k, "loader", "dlm_usage", map[string]interface{}{
			"warning": ceWarning,
			"prefix":  m.k.Prefix(),
		}))
	}

	// Show wait indicator.
	_ = m.edit(ctx, ev, sf(m.k, "loader", "wait", map[string]interface{}{"wait": ceWait}))

	// -list [name]
	if args[0] == "-list" {
		if len(args) == 1 {
			return m.cmdDlmListAll(ctx, ev)
		}
		return m.cmdDlmInfo(ctx, ev, args[1])
	}

	// -send / -s / --send  <name> [repo_index]
	if args[0] == "-send" || args[0] == "-s" || args[0] == "--send" {
		if len(args) < 2 {
			return m.edit(ctx, ev, sf(m.k, "loader", "dlm_send_usage", map[string]interface{}{
				"warning": ceWarning,
				"prefix":  m.k.Prefix(),
			}))
		}
		moduleOrURL := args[1]
		var repoIndex *int
		if len(args) > 2 {
			var idx int
			if _, err := fmt.Sscanf(args[2], "%d", &idx); err == nil {
				r := idx - 1
				repoIndex = &r
			}
		}
		return m.runDlmInstall(ctx, ev, moduleOrURL, true, repoIndex)
	}

	// <module_name_or_URL> [repo_index]
	moduleOrURL := args[0]
	var repoIndex *int
	if len(args) > 1 {
		var idx int
		if _, err := fmt.Sscanf(args[1], "%d", &idx); err == nil {
			r := idx - 1
			repoIndex = &r
		}
	}

	// If not a URL and no explicit repo index, find which repos have the module.
	if !isURL(moduleOrURL) && repoIndex == nil {
		matches := m.findRepoMatches(ctx, moduleOrURL)
		if len(matches) > 1 {
			// Multiple repos: let user choose. Show a text-based choice prompt.
			_ = m.edit(ctx, ev, m.buildRepoChoiceText(moduleOrURL, matches))
			return nil
		}
		if len(matches) == 1 {
			idx := matches[0].Index
			repoIndex = &idx
		}
	}

	return m.runDlmInstall(ctx, ev, moduleOrURL, false, repoIndex)
}

// buildRepoChoiceText creates a text message showing which repos have the module.
// The user can then re-run `.dlm <name> <repo_number>` to select.
func (m *loaderModule) buildRepoChoiceText(moduleName string, matches []repoMatch) string {
	var sb strings.Builder
	sb.WriteString(sf(m.k, "loader", "dlm_repo_choice_title", map[string]interface{}{
		"cloud":       ceCloud,
		"module_name": moduleName,
		"action":      s(m.k, "loader", "dlm_repo_choice_action_install"),
	}))
	sb.WriteString("\n")
	for _, match := range matches {
		sb.WriteString(sf(m.k, "loader", "dlm_repo_choice_repo", map[string]interface{}{
			"index":     match.Index + 1,
			"repo_name": match.URL,
		}) + "\n")
	}
	sb.WriteString(fmt.Sprintf("\n<i>Use: <code>%sdlm %s &lt;number&gt;</code></i>",
		m.k.Prefix(), moduleName))
	return sb.String()
}

// cmdDlmListAll lists all modules from all repos.
// Mirrors the `-list` no-name branch in cmd_dlm().
func (m *loaderModule) cmdDlmListAll(ctx context.Context, ev *events.NewMessage) error {
	_ = m.edit(ctx, ev, sf(m.k, "loader", "dlm_list_loading", map[string]interface{}{"loading": ceLoading}))

	repos := m.allRepoURLs()
	var messageLines []string
	var errs []string

	for i, repo := range repos {
		mods, err := m.repoMgr.GetModuleList(ctx, repo)
		repoName := repoShortName(repo)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%d. %s: ошибка - %s", i+1, repoName, err.Error()[:min(50, len(err.Error()))]))
			continue
		}
		if len(mods) > 0 {
			messageLines = append(messageLines, fmt.Sprintf("<b>%s</b>: %s", repoName, strings.Join(mods, " | ")))
		} else {
			errs = append(errs, fmt.Sprintf("%d. %s: пустой список", i+1, repoName))
		}
	}

	var finalMsg string
	if len(messageLines) > 0 {
		finalMsg = sf(m.k, "loader", "dlm_list_title", map[string]interface{}{
			"folder": ceFolder,
			"list":   strings.Join(messageLines, "\n"),
		})
		if len(errs) > 0 {
			finalMsg += sf(m.k, "loader", "dlm_list_errors", map[string]interface{}{
				"warning": ceWarning,
				"errors":  strings.Join(errs, "<br>"),
			})
		}
	} else {
		finalMsg = sf(m.k, "loader", "dlm_list_failed", map[string]interface{}{"warning": ceWarning})
		if len(errs) > 0 {
			finalMsg += "\n<blockquote expandable>" + strings.Join(errs, "<br>") + "</blockquote>"
		}
	}
	return m.edit(ctx, ev, finalMsg)
}

// cmdDlmInfo shows info about a specific module from repos.
// Mirrors the `-list <name>` branch in cmd_dlm().
func (m *loaderModule) cmdDlmInfo(ctx context.Context, ev *events.NewMessage, moduleName string) error {
	_ = m.edit(ctx, ev, sf(m.k, "loader", "dlm_searching", map[string]interface{}{
		"loading":     ceLoading,
		"module_name": moduleName,
	}))

	code, repoURL, err := m.downloadFromRepos(ctx, moduleName)
	if err != nil {
		return m.edit(ctx, ev, sf(m.k, "loader", "module_not_found", map[string]interface{}{
			"warning":     ceWarning,
			"module_name": moduleName,
		}))
	}

	md := parseMetadata(code)
	size := len([]byte(code))
	return m.edit(ctx, ev, sf(m.k, "loader", "module_info", map[string]interface{}{
		"file":        ceFile,
		"module_name": moduleName,
		"idea":        ceIdea,
		"description": md.Desc,
		"crystal":     ceCrystal,
		"version":     md.Version,
		"angel":       ceAngel,
		"author":      md.Author,
		"folder":      ceFolder,
		"size":        size,
		"cloud":       ceCloud,
		"repo":        repoURL,
	}))
}

// ─────────────────────────────────────────────────────────────────────────────
// .um
// ─────────────────────────────────────────────────────────────────────────────

// cmdUm unloads one or more modules by name (comma-separated allowed).
// Mirrors cmd_um() in loader.py.
func (m *loaderModule) cmdUm(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	argsRaw := m.parseArgsRaw(ev)
	if argsRaw == "" {
		return m.edit(ctx, ev, sf(m.k, "loader", "um_usage", map[string]interface{}{
			"warning": ceWarning,
			"prefix":  m.k.Prefix(),
		}))
	}

	// Support comma-separated module names (as in Python).
	rawNames := strings.Split(argsRaw, ",")
	var moduleNames []string
	for _, n := range rawNames {
		if trimmed := strings.TrimSpace(n); trimmed != "" {
			moduleNames = append(moduleNames, trimmed)
		}
	}

	var success []string
	var failed []string

	for _, rawName := range moduleNames {
		actualName, found := m.findModuleCaseInsensitive(rawName)
		if !found {
			failed = append(failed, sf(m.k, "loader", "um_not_found", map[string]interface{}{"module_name": rawName}))
			continue
		}
		moduleName := actualName

		// System module protection.
		if m.cfg.ProtectSystem {
			if _, isSys := m.k.SystemModules[moduleName]; isSys {
				failed = append(failed, sf(m.k, "loader", "um_system_module", map[string]interface{}{"module_name": moduleName}))
				continue
			}
		}

		// Unregister commands.
		if mod, ok := m.k.LoadedModules[moduleName]; ok {
			for _, cmd := range mod.Commands() {
				m.k.UnregisterCommand(cmd.Name)
			}
		}
		if err := m.k.Loader.Unload(moduleName); err != nil && m.k.Log != nil {
			m.k.Log.Warn("um: unload %q: %v", moduleName, err)
		}
		delete(m.k.LoadedModules, moduleName)

		// Delete file.
		filePath := m.findModuleFileAny(moduleName)
		if filePath != "" {
			_ = os.Remove(filePath)
		}

		if m.k.ModuleSources != nil {
			delete(m.k.ModuleSources, moduleName)
		}
		delete(m.srcInfoMap, moduleName)
		success = append(success, moduleName)
	}

	var parts []string
	if len(success) > 0 {
		names := strings.Join(func() []string {
			out := make([]string, len(success))
			for i, n := range success {
				out[i] = "<code>" + n + "</code>"
			}
			return out
		}(), ", ")
		parts = append(parts, sf(m.k, "loader", "um_success_header", map[string]interface{}{
			"success": ceSuccess,
			"count":   len(success),
		})+"\n<blockquote>"+names+"</blockquote>")
	}
	if len(failed) > 0 {
		parts = append(parts, sf(m.k, "loader", "um_failed_header", map[string]interface{}{
			"blocked": ceBlocked,
			"count":   len(failed),
		})+"\n<blockquote>"+strings.Join(failed, "\n")+"</blockquote>")
	}

	return m.edit(ctx, ev, strings.Join(parts, "\n"))
}

// ─────────────────────────────────────────────────────────────────────────────
// .unlm
// ─────────────────────────────────────────────────────────────────────────────

// cmdUnlm sends the .py file of a loaded module to the current chat.
// Mirrors cmd_unlm() in loader.py.
func (m *loaderModule) cmdUnlm(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 {
		return m.edit(ctx, ev, sf(m.k, "loader", "unlm_usage", map[string]interface{}{
			"warning": ceWarning,
			"prefix":  m.k.Prefix(),
		}))
	}
	moduleName := args[0]
	actualName, found := m.findModuleCaseInsensitive(moduleName)
	if !found {
		return m.edit(ctx, ev, sf(m.k, "loader", "module_not_found_um", map[string]interface{}{
			"warning":     ceWarning,
			"module_name": moduleName,
		}))
	}
	moduleName = actualName

	filePath := m.findModuleFileAny(moduleName)
	if filePath == "" {
		return m.edit(ctx, ev, sf(m.k, "loader", "module_file_not_found", map[string]interface{}{"warning": ceWarning}))
	}

	_ = m.edit(ctx, ev, sf(m.k, "loader", "uploading_module", map[string]interface{}{
		"upload":      ceUpload,
		"module_name": moduleName,
	}))

	srcLink := m.getSourceLink(moduleName)
	caption := sf(m.k, "loader", "file_upload_caption", map[string]interface{}{
		"file":        ceFile,
		"module_name": moduleName,
		"prefix":      m.k.Prefix(),
		"source_link": srcLink,
	})
	if err := sendDocument(ctx, m.k, ev.PeerID, filePath, caption); err != nil {
		return m.edit(ctx, ev, ceError+" Send failed: "+err.Error())
	}
	return m.edit(ctx, ev, caption)
}

// ─────────────────────────────────────────────────────────────────────────────
// .reload
// ─────────────────────────────────────────────────────────────────────────────

// cmdReload reloads one or all user modules from their files.
// Mirrors cmd_reload() in loader.py.
func (m *loaderModule) cmdReload(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	// Reload all loaded modules.
	if len(args) == 0 {
		modules := m.loadedModuleNames()
		if len(modules) == 0 {
			return m.edit(ctx, ev, sf(m.k, "loader", "no_modules", map[string]interface{}{"folder": ceFolder}))
		}

		_ = m.edit(ctx, ev, sf(m.k, "loader", "reload_all", map[string]interface{}{"reload": ceReload}))

		var results []string
		var failed []string

		for _, modName := range modules {
			filePath := m.findModuleFileAny(modName)
			if filePath == "" {
				failed = append(failed, modName)
				continue
			}

			// Unregister old commands.
			if mod, ok := m.k.LoadedModules[modName]; ok {
				for _, cmd := range mod.Commands() {
					m.k.UnregisterCommand(cmd.Name)
				}
				delete(m.k.LoadedModules, modName)
			}

			// Re-load.
			if m.k.Loader.PyLoaderInstance() != nil {
				if err := m.k.Loader.LoadPyFile(filePath); err != nil {
					failed = append(failed, modName)
					continue
				}
			} else {
				failed = append(failed, modName)
				continue
			}
			results = append(results, modName)
		}

		successCount := len(results)
		failedCount := len(failed)

		if failedCount > 0 {
			var failedList strings.Builder
			for _, name := range failed {
				if failedList.Len() >= 10 {
					break
				}
				failedList.WriteString(sf(m.k, "loader", "failed_module", map[string]interface{}{"name": name}))
			}
			if failedCount > 10 {
				failedList.WriteString(sf(m.k, "loader", "and_more", map[string]interface{}{"count": failedCount - 10}))
			}

			if successCount > 0 {
				return m.edit(ctx, ev, sf(m.k, "loader", "reload_all_partial", map[string]interface{}{
					"success":       ceSuccess,
					"success_count": fmt.Sprintf("✓ %d", successCount),
					"warning":       ceWarning,
					"failed_count":  failedCount,
					"failed_list":   failedList.String(),
				}))
			}
			return m.edit(ctx, ev, sf(m.k, "loader", "reload_all_failed", map[string]interface{}{
				"warning":     ceWarning,
				"count":       failedCount,
				"failed_list": failedList.String(),
			}))
		}

		if successCount == 1 {
			return m.edit(ctx, ev, sf(m.k, "loader", "reload_all_success_one", map[string]interface{}{
				"success": ceSuccess,
				"count":   "1",
				"name":    results[0],
			}))
		}
		return m.edit(ctx, ev, sf(m.k, "loader", "reload_all_success", map[string]interface{}{
			"success": ceSuccess,
			"count":   fmt.Sprintf("✓ %d", successCount),
		}))
	}

	// Single module reload.
	moduleName := args[0]
	actualName, found := m.findModuleCaseInsensitive(moduleName)
	if !found {
		return m.edit(ctx, ev, sf(m.k, "loader", "module_not_found_um", map[string]interface{}{
			"warning":     ceWarning,
			"module_name": moduleName,
		}))
	}
	moduleName = actualName

	filePath := m.findModuleFileAny(moduleName)
	if filePath == "" {
		return m.edit(ctx, ev, sf(m.k, "loader", "module_file_not_found", map[string]interface{}{"warning": ceWarning}))
	}

	_ = m.edit(ctx, ev, sf(m.k, "loader", "reloading", map[string]interface{}{
		"reload":      ceReload,
		"module_name": moduleName,
	}))

	// Install deps if needed.
	if src, err := os.ReadFile(filePath); err == nil { // #nosec G304
		code := string(src)
		if pl := m.k.Loader.PyLoaderInstance(); pl != nil {
			if deps := pl.ParseRequires(code); len(deps) > 0 {
				var depsEmoji strings.Builder
				for _, dep := range deps {
					depsEmoji.WriteString(ceLib + " " + dep + "\n")
				}
				_ = m.edit(ctx, ev, sf(m.k, "loader", "installing_deps", map[string]interface{}{
					"dependencies": ceDependencies,
					"deps_list":    depsEmoji.String(),
				}))
				_ = pl.InstallRequires(deps)
			}
		}
	}

	// Unregister old commands.
	if mod, ok := m.k.LoadedModules[moduleName]; ok {
		for _, cmd := range mod.Commands() {
			m.k.UnregisterCommand(cmd.Name)
		}
		delete(m.k.LoadedModules, moduleName)
	}

	// Reload.
	if m.k.Loader.PyLoaderInstance() == nil {
		return m.edit(ctx, ev, sf(m.k, "loader", "reload_error", map[string]interface{}{"warning": ceWarning}))
	}
	if err := m.k.Loader.LoadPyFile(filePath); err != nil {
		return m.edit(ctx, ev, sf(m.k, "loader", "reload_error", map[string]interface{}{"warning": ceWarning}))
	}

	// Build success.
	var modCmds []loader.Command
	if mod, ok := m.k.LoadedModules[moduleName]; ok {
		modCmds = mod.Commands()
	}

	var cmdText string
	if len(modCmds) > 0 {
		var cmdParts []string
		for _, cmd := range modCmds {
			cmdParts = append(cmdParts, "<code>"+m.k.Prefix()+cmd.Name+"</code>")
		}
		cmdText = ceCrystal + " " + strings.Join(cmdParts, ", ")
	} else {
		cmdText = s(m.k, "loader", "no_commands")
	}

	emoji := pickRandomEmoji()
	return m.edit(ctx, ev, sf(m.k, "loader", "reload_success", map[string]interface{}{
		"success":     ceSuccess,
		"module_name": moduleName,
		"emoji":       emoji,
		"cmd_text":    cmdText,
	}))
}

// ─────────────────────────────────────────────────────────────────────────────
// .addrepo / .delrepo
// ─────────────────────────────────────────────────────────────────────────────

// cmdAddrepo adds a module repository URL.
// Mirrors cmd_addrepo() in loader.py.
func (m *loaderModule) cmdAddrepo(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	rawURL := strings.TrimSpace(m.parseArgsRaw(ev))
	if rawURL == "" {
		return m.edit(ctx, ev, sf(m.k, "loader", "addrepo_usage", map[string]interface{}{
			"warning": ceWarning,
			"prefix":  m.k.Prefix(),
		}))
	}

	if err := m.repoMgr.AddRepo(ctx, rawURL); err != nil {
		return m.edit(ctx, ev, ceWarning+" <b>"+err.Error()+"</b>")
	}
	m.saveRepos()
	return m.edit(ctx, ev, ceSuccess+" <b>Repository added: <code>"+rawURL+"</code></b>")
}

// cmdDelrepo removes a module repository by index.
// Mirrors cmd_delrepo() in loader.py.
func (m *loaderModule) cmdDelrepo(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 {
		return m.edit(ctx, ev, sf(m.k, "loader", "delrepo_usage", map[string]interface{}{
			"warning": ceWarning,
			"prefix":  m.k.Prefix(),
		}))
	}

	var idx int
	if _, err := fmt.Sscanf(args[0], "%d", &idx); err != nil {
		return m.edit(ctx, ev, sf(m.k, "loader", "delrepo_usage", map[string]interface{}{
			"warning": ceWarning,
			"prefix":  m.k.Prefix(),
		}))
	}
	if err := m.repoMgr.RemoveRepo(idx); err != nil {
		return m.edit(ctx, ev, ceWarning+" <b>"+err.Error()+"</b>")
	}
	m.saveRepos()
	return m.edit(ctx, ev, ceSuccess+fmt.Sprintf(" <b>Repository #%d removed.</b>", idx))
}

// ─────────────────────────────────────────────────────────────────────────────
// Utility functions
// ─────────────────────────────────────────────────────────────────────────────

// repoShortName extracts a readable name from a repo URL.
func repoShortName(repoURL string) string {
	parts := strings.Split(strings.TrimRight(repoURL, "/"), "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return repoURL
}

// ifStr returns a if cond is true, else b.
func ifStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

// fileSize returns the size of a file in bytes.
func fileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
