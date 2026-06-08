// Package modules – modules.go
//
// loaderModule is the Go port of loader.py. It provides the module
// management commands: .iload, .dlm, .um, .unlm, .reload, .addrepo, .delrepo.
package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

const (
	dbKeyLoaderRepos = "loader:repos"

	defaultRepo = "https://raw.githubusercontent.com/hairpin01/MCUB-fork/main/modules"
)

// ---------------------------------------------------------------------------
// Loader emoji constants (from loader.py CUSTOM_EMOJI dict / task spec)
// ---------------------------------------------------------------------------

const (
	loaderEmojiLoading = "<tg-emoji emoji-id=\"5310041868191407556\">⏳</tg-emoji>"
	loaderEmojiSuccess = "<tg-emoji emoji-id=\"5332654441508119011\">✅</tg-emoji>"
	loaderEmojiError   = "<tg-emoji emoji-id=\"5388785832956016892\">❌</tg-emoji>"
	loaderEmojiIdea    = "<tg-emoji emoji-id=\"5424905419286601547\">💡</tg-emoji>"
	loaderEmojiFile    = "<tg-emoji emoji-id=\"5433653135799228968\">📁</tg-emoji>"
	loaderEmojiCrystal = "<tg-emoji emoji-id=\"5361837567463399422\">🔮</tg-emoji>"
	loaderEmojiAngel   = "<tg-emoji emoji-id=\"5420315771499551346\">😇</tg-emoji>"
	loaderEmojiAuthor  = "<tg-emoji emoji-id=\"5373004843210251169\">🥞</tg-emoji>"
	loaderEmojiBlock   = "<tg-emoji emoji-id=\"5767151002666929821\">🚫</tg-emoji>"
	loaderEmojiCloud   = "<tg-emoji emoji-id=\"5321304062715517873\">☁️</tg-emoji>"
	loaderEmojiWarning = "<tg-emoji emoji-id=\"5409235172979672859\">⚠️</tg-emoji>"
	loaderEmojiDeps    = "<tg-emoji emoji-id=\"5328311576736833844\">🟠</tg-emoji>"
)

// loaderModule provides module-management commands ported from loader.py.
type loaderModule struct {
	k       *kernel.Kernel
	repoMgr *loader.RepositoryManager
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

	// Restore persisted user repos from DB.
	if kern.DB != nil {
		if saved, ok2, err := kern.DB.ModuleGet("loader", "repos"); err == nil && ok2 && saved != "" {
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
func (m *loaderModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "iload",
			Description: "reply to .py or .zip — install module from file",
			Handler:     m.cmdIload,
		},
		{
			Name:        "dlm",
			Description: "[name|URL|-list|-send name] — download/install/list modules",
			Handler:     m.cmdDlm,
		},
		{
			Name:        "um",
			Description: "<name> — unload a module",
			Handler:     m.cmdUm,
		},
		{
			Name:        "unlm",
			Description: "<name> — send module .py file to chat",
			Handler:     m.cmdUnlm,
		},
		{
			Name:        "reload",
			Description: "[name] — reload module(s)",
			Handler:     m.cmdReload,
		},
		{
			Name:        "addrepo",
			Description: "<url> — add a module repository",
			Handler:     m.cmdAddrepo,
		},
		{
			Name:        "delrepo",
			Description: "<id> — remove a repository by index",
			Handler:     m.cmdDelrepo,
		},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

// replyFilename returns the filename of the document attached to the replied
// message, or "" when there is no document.
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

// downloadReplyFile downloads the document from the replied message to destPath.
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

// isURL returns true when s looks like an HTTP/HTTPS URL.
func isURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https")
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
		_ = m.k.DB.ModuleSet("loader", "repos", string(data))
	}
}

// findModuleFile searches modules_loaded/ for a .py file whose stem matches name.
func (m *loaderModule) findModuleFile(name string) string {
	dir := m.k.ModulesLoadedDir
	if dir == "" {
		dir = "modules_loaded"
	}
	// Exact filename match first.
	exact := filepath.Join(dir, name+".py")
	if _, err := os.Stat(exact); err == nil {
		return exact
	}
	// Case-insensitive search.
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

// loadedModuleNames returns a sorted list of user-loaded module names.
func (m *loaderModule) loadedModuleNames() []string {
	names := make([]string, 0, len(m.k.LoadedModules))
	for n := range m.k.LoadedModules {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ---------------------------------------------------------------------------
// .iload
// ---------------------------------------------------------------------------

// cmdIload installs a module from the .py (or .zip) file attached to the
// replied message.
func (m *loaderModule) cmdIload(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}
	if ev.ReplyToMsgID == 0 {
		return m.edit(ctx, ev, "⚠️ Reply to a message containing a <code>.py</code> or <code>.zip</code> file.")
	}

	fileName, err := m.replyFilename(ctx, ev)
	if err != nil {
		return m.edit(ctx, ev, fmt.Sprintf("❌ Could not fetch replied message: %v", err))
	}
	if fileName == "" {
		return m.edit(ctx, ev, "⚠️ Replied message has no document.")
	}

	ext := strings.ToLower(filepath.Ext(fileName))
	if ext != ".py" && ext != ".zip" {
		return m.edit(ctx, ev, fmt.Sprintf("⚠️ Expected <code>.py</code> or <code>.zip</code>, got <code>%s</code>.", fileName))
	}

	_ = m.edit(ctx, ev, loaderEmojiLoading+" <b>Please wait...</b>\n"+loaderEmojiFile+" Downloading <code>"+fileName+"</code>…")

	destDir := m.k.ModulesLoadedDir
	if destDir == "" {
		destDir = "modules_loaded"
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Cannot create modules directory: <code>"+err.Error()+"</code>")
	}

	tmpPath := filepath.Join(destDir, fileName)
	if err := m.downloadReplyFile(ctx, ev, tmpPath); err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Download failed: <code>"+err.Error()+"</code>")
	}

	if ext == ".zip" {
		return m.loadArchive(ctx, ev, tmpPath, destDir)
	}
	return m.loadPyFile(ctx, ev, tmpPath)
}

// loadArchive extracts zipPath and loads each .py module found inside.
func (m *loaderModule) loadArchive(ctx context.Context, ev *events.NewMessage, zipPath, destDir string) error {
	_ = m.edit(ctx, ev, loaderEmojiLoading+" Extracting archive…")

	result, err := loader.ExtractArchive(zipPath, destDir)
	if err != nil {
		_ = os.Remove(zipPath)
		return m.edit(ctx, ev, loaderEmojiError+" Extraction failed: <code>"+err.Error()+"</code>")
	}
	_ = os.Remove(zipPath)

	if len(result.Modules) == 0 {
		return m.edit(ctx, ev, loaderEmojiWarning+" No <code>.py</code> modules found in archive.")
	}

	var loaded []string
	var failed []string
	for _, mod := range result.Modules {
		_ = m.edit(ctx, ev, loaderEmojiLoading+" Installing <code>"+mod.Name+"</code>…")
		if err2 := m.k.Loader.LoadPyFile(mod.FilePath); err2 != nil {
			failed = append(failed, fmt.Sprintf("%s (%v)", mod.Name, err2))
		} else {
			loaded = append(loaded, mod.Name)
		}
	}

	var sb strings.Builder
	if len(loaded) > 0 {
		sb.WriteString(loaderEmojiSuccess + " Loaded from archive: " + strings.Join(loaded, ", "))
	}
	if len(failed) > 0 {
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(loaderEmojiError + " Failed: " + strings.Join(failed, "; "))
	}
	return m.edit(ctx, ev, sb.String())
}

// loadPyFile loads a single .py file, checks for pip dependencies (already
// handled by PyLoader.LoadPyFile), and reports the result using the
// module_loaded langpack template.
func (m *loaderModule) loadPyFile(ctx context.Context, ev *events.NewMessage, pyPath string) error {
	baseName := filepath.Base(pyPath)
	stem := strings.TrimSuffix(baseName, ".py")

	// Check for # requires: lines in the source so we can show a deps message.
	if srcBytes, readErr := os.ReadFile(pyPath); readErr == nil { // #nosec G304
		pl := m.k.Loader.PyLoaderInstance()
		if pl != nil {
			if deps := pl.ParseRequires(string(srcBytes)); len(deps) > 0 {
				_ = m.edit(ctx, ev,
					loaderEmojiDeps+" <b>Installing dependencies:</b>\n<blockquote><code>"+
						strings.Join(deps, "\n")+"</code></blockquote>")
			}
		}
	}

	_ = m.edit(ctx, ev, loaderEmojiLoading+" Installing <b>"+stem+"</b>…")

	if err := m.k.Loader.LoadPyFile(pyPath); err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Load failed: <code>"+err.Error()+"</code>")
	}

	// Build success message using the module_loaded template.
	// Try to find the loaded module in LoadedModules by stem name.
	var cmdLines []string
	modName := stem
	for name, mod := range m.k.LoadedModules {
		if strings.EqualFold(name, stem) || strings.EqualFold(strings.TrimPrefix(name, "userbot-"), stem) {
			modName = name
			for _, c := range mod.Commands() {
				cmdLines = append(cmdLines, m.k.Prefix()+c.Name+" — "+c.Description)
			}
			break
		}
	}

	var sb strings.Builder
	// {success} <b>Module</b> <code>{module_name}</code> <b>loaded!</b>
	sb.WriteString(loaderEmojiSuccess + " <b>Module</b> <code>" + modName + "</code> <b>loaded!</b> " + loaderEmojiCrystal)

	if len(cmdLines) > 0 {
		sb.WriteString("\n<blockquote>" + loaderEmojiIdea + " <b>Commands:</b>\n")
		sb.WriteString(strings.Join(cmdLines, "\n"))
		sb.WriteString("</blockquote>")
	}

	return m.edit(ctx, ev, sb.String())
}

// ---------------------------------------------------------------------------
// .dlm
// ---------------------------------------------------------------------------

// cmdDlm handles .dlm [-list|-send] [name|URL]
func (m *loaderModule) cmdDlm(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	args := m.parseArgs(ev)

	// .dlm with no args → list all repos
	if len(args) == 0 {
		return m.cmdDlmList(ctx, ev, "")
	}

	switch args[0] {
	case "-list":
		filter := ""
		if len(args) > 1 {
			filter = strings.Join(args[1:], " ")
		}
		return m.cmdDlmList(ctx, ev, filter)

	case "-send":
		if len(args) < 2 {
			return m.edit(ctx, ev, "⚠️ Usage: <code>dlm -send &lt;name&gt;</code>")
		}
		name := strings.Join(args[1:], " ")
		return m.cmdDlmSend(ctx, ev, name)

	default:
		// .dlm <name|URL>
		target := strings.Join(args, " ")
		return m.cmdDlmInstall(ctx, ev, target)
	}
}

// cmdDlmList fetches and shows module lists from all repos.
func (m *loaderModule) cmdDlmList(ctx context.Context, ev *events.NewMessage, filter string) error {
	_ = m.edit(ctx, ev, loaderEmojiLoading+" Fetching module lists…")

	repos := m.allRepoURLs()
	var sb strings.Builder
	sb.WriteString(loaderEmojiCloud + " <b>Available modules:</b>\n")

	for i, repoURL := range repos {
		mods, err := m.repoMgr.GetModuleList(ctx, repoURL)
		repoLabel := fmt.Sprintf("[%d] <code>%s</code>", i, repoURL)
		if err != nil {
			sb.WriteString("\n" + repoLabel + " — " + loaderEmojiError + " " + err.Error() + "\n")
			continue
		}
		count := 0
		for _, mod := range mods {
			if filter == "" || strings.Contains(strings.ToLower(mod), strings.ToLower(filter)) {
				count++
			}
		}
		sb.WriteString(fmt.Sprintf("\n"+loaderEmojiFile+" %s (%d modules):\n", repoLabel, count))
		for _, mod := range mods {
			if filter != "" && !strings.Contains(strings.ToLower(mod), strings.ToLower(filter)) {
				continue
			}
			sb.WriteString("  • <code>" + mod + "</code>\n")
		}
	}
	return m.edit(ctx, ev, sb.String())
}

// cmdDlmSend downloads a module from a repo and sends it to chat without installing.
func (m *loaderModule) cmdDlmSend(ctx context.Context, ev *events.NewMessage, name string) error {
	_ = m.edit(ctx, ev, loaderEmojiLoading+" Fetching <code>"+name+"</code>…")

	code, repoURL, err := m.downloadFromRepos(ctx, name)
	if err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Module <code>"+name+"</code> not found in repos: "+err.Error())
	}
	_ = repoURL

	tmpPath := filepath.Join(os.TempDir(), name+".py")
	if err2 := os.WriteFile(tmpPath, []byte(code), 0o644); err2 != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Write tmp file: "+err2.Error())
	}
	defer os.Remove(tmpPath)

	_ = m.edit(ctx, ev, loaderEmojiFile+" Sending <code>"+name+".py</code>…")
	if _, err2 := m.k.Client.SendDocument(ctx, ev.PeerID, tmpPath, name+".py"); err2 != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Send failed: "+err2.Error())
	}
	return m.edit(ctx, ev, loaderEmojiSuccess+" Sent <code>"+name+".py</code>.")
}

// cmdDlmInstall downloads a module from a repo (or URL) and installs it.
func (m *loaderModule) cmdDlmInstall(ctx context.Context, ev *events.NewMessage, target string) error {
	destDir := m.k.ModulesLoadedDir
	if destDir == "" {
		destDir = "modules_loaded"
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Cannot create modules directory: "+err.Error())
	}

	if isURL(target) {
		_ = m.edit(ctx, ev, loaderEmojiLoading+" Downloading from URL…")
		return m.installFromURL(ctx, ev, target, destDir)
	}

	_ = m.edit(ctx, ev, loaderEmojiLoading+" Searching repos for <code>"+target+"</code>…")

	code, repoURL, err := m.downloadFromRepos(ctx, target)
	if err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Module <code>"+target+"</code> not found: "+err.Error())
	}
	_ = repoURL

	pyPath := filepath.Join(destDir, target+".py")
	if err2 := os.WriteFile(pyPath, []byte(code), 0o644); err2 != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Write module file: "+err2.Error())
	}

	return m.loadPyFile(ctx, ev, pyPath)
}

// installFromURL downloads a .py from rawURL, saves it to destDir, and loads it.
func (m *loaderModule) installFromURL(ctx context.Context, ev *events.NewMessage, rawURL, destDir string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Build request: "+err.Error())
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Download: "+err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return m.edit(ctx, ev, fmt.Sprintf(loaderEmojiError+" HTTP %d from URL.", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Read body: "+err.Error())
	}

	base := filepath.Base(rawURL)
	if !strings.HasSuffix(strings.ToLower(base), ".py") {
		base += ".py"
	}
	pyPath := filepath.Join(destDir, base)
	if err2 := os.WriteFile(pyPath, data, 0o644); err2 != nil {
		return m.edit(ctx, ev, loaderEmojiError+" Write module file: "+err2.Error())
	}
	return m.loadPyFile(ctx, ev, pyPath)
}

// downloadFromRepos tries each repo in order and returns the source code and
// the URL it was found at.
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

// ---------------------------------------------------------------------------
// .um
// ---------------------------------------------------------------------------

func (m *loaderModule) cmdUm(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 {
		return m.edit(ctx, ev, "⚠️ Usage: <code>um &lt;module-name&gt;</code>")
	}
	name := args[0]

	if _, isSys := m.k.SystemModules[name]; isSys {
		return m.edit(ctx, ev, fmt.Sprintf("⚠️ Cannot unload system module <code>%s</code>.", name))
	}
	mod, ok := m.k.LoadedModules[name]
	if !ok {
		return m.edit(ctx, ev, fmt.Sprintf("❌ Module <code>%s</code> is not loaded.", name))
	}

	for _, cmd := range mod.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	if err := m.k.Loader.Unload(name); err != nil && m.k.Log != nil {
		m.k.Log.Warn("um: unload %q: %v", name, err)
	}
	delete(m.k.LoadedModules, name)
	delete(m.k.ModuleSources, name)

	return m.edit(ctx, ev, fmt.Sprintf("✅ Module <code>%s</code> unloaded.", name))
}

// ---------------------------------------------------------------------------
// .unlm
// ---------------------------------------------------------------------------

// cmdUnlm sends the .py file of a loaded user module to the current chat.
func (m *loaderModule) cmdUnlm(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 {
		return m.edit(ctx, ev, "⚠️ Usage: <code>unlm &lt;module-name&gt;</code>")
	}
	name := args[0]

	pyPath := m.findModuleFile(name)
	if pyPath == "" {
		return m.edit(ctx, ev, fmt.Sprintf("❌ Module file for <code>%s</code> not found.", name))
	}

	_ = m.edit(ctx, ev, fmt.Sprintf("📤 Sending <code>%s</code>…", filepath.Base(pyPath)))
	if _, err := m.k.Client.SendDocument(ctx, ev.PeerID, pyPath, filepath.Base(pyPath)); err != nil {
		return m.edit(ctx, ev, fmt.Sprintf("❌ Send failed: %v", err))
	}
	return m.edit(ctx, ev, fmt.Sprintf("✅ Sent <code>%s</code>.", filepath.Base(pyPath)))
}

// ---------------------------------------------------------------------------
// .reload
// ---------------------------------------------------------------------------

func (m *loaderModule) cmdReload(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	if len(args) == 0 {
		// Reload all user modules.
		names := m.loadedModuleNames()
		if len(names) == 0 {
			return m.edit(ctx, ev, "ℹ️ No user modules to reload.")
		}
		_ = m.edit(ctx, ev, fmt.Sprintf("⏳ Reloading %d module(s)…", len(names)))
		var failed []string
		for _, n := range names {
			if err := m.softReload(n); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", n, err))
			}
		}
		if len(failed) > 0 {
			return m.edit(ctx, ev, fmt.Sprintf(
				"⚠️ Reloaded %d/%d. Errors:\n%s",
				len(names)-len(failed), len(names), strings.Join(failed, "\n"),
			))
		}
		return m.edit(ctx, ev, fmt.Sprintf("✅ Reloaded %d module(s).", len(names)))
	}

	name := args[0]
	if _, ok := m.findModule(name); !ok {
		return m.edit(ctx, ev, fmt.Sprintf("❌ Module <code>%s</code> not found.", name))
	}
	if err := m.softReload(name); err != nil {
		return m.edit(ctx, ev, fmt.Sprintf("❌ Reload of <code>%s</code> failed: %v", name, err))
	}
	return m.edit(ctx, ev, fmt.Sprintf("✅ Module <code>%s</code> reloaded.", name))
}

func (m *loaderModule) findModule(name string) (loader.Module, bool) {
	if mod, ok := m.k.SystemModules[name]; ok {
		return mod, true
	}
	if mod, ok := m.k.LoadedModules[name]; ok {
		return mod, true
	}
	return nil, false
}

func (m *loaderModule) softReload(name string) error {
	mod, ok := m.findModule(name)
	if !ok {
		return fmt.Errorf("module %q not found", name)
	}
	for _, cmd := range mod.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	if err := mod.OnUnload(m.k); err != nil && m.k.Log != nil {
		m.k.Log.Warn("reload: OnUnload %q: %v", name, err)
	}
	if err := mod.OnLoad(m.k); err != nil {
		return fmt.Errorf("OnLoad: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// .addrepo / .delrepo
// ---------------------------------------------------------------------------

func (m *loaderModule) cmdAddrepo(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	rawURL := strings.TrimSpace(m.parseArgsRaw(ev))
	if rawURL == "" {
		return m.edit(ctx, ev, "⚠️ Usage: <code>addrepo &lt;url&gt;</code>")
	}

	_ = m.edit(ctx, ev, "⏳ Validating repository…")
	if err := m.repoMgr.AddRepo(ctx, rawURL); err != nil {
		return m.edit(ctx, ev, fmt.Sprintf("❌ %v", err))
	}
	m.saveRepos()

	repos := m.repoMgr.ListRepos()
	return m.edit(ctx, ev, fmt.Sprintf(
		"✅ Repository added (#%d): <code>%s</code>",
		len(repos)-1, rawURL,
	))
}

func (m *loaderModule) cmdDelrepo(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 {
		return m.showRepoList(ctx, ev)
	}

	var idx int
	if _, err := fmt.Sscanf(args[0], "%d", &idx); err != nil {
		return m.edit(ctx, ev, "⚠️ Usage: <code>delrepo &lt;id&gt;</code> (integer index from .delrepo list)")
	}
	if err := m.repoMgr.RemoveRepo(idx); err != nil {
		return m.edit(ctx, ev, fmt.Sprintf("❌ %v", err))
	}
	m.saveRepos()
	return m.edit(ctx, ev, fmt.Sprintf("✅ Repository #%d removed.", idx))
}

func (m *loaderModule) showRepoList(ctx context.Context, ev *events.NewMessage) error {
	repos := m.repoMgr.ListRepos()
	var sb strings.Builder
	sb.WriteString("<b>Repositories:</b>\n")
	sb.WriteString(fmt.Sprintf("  #0 (default): <code>%s</code>\n", defaultRepo))
	for i, r := range repos {
		sb.WriteString(fmt.Sprintf("  #%d: <code>%s</code>\n", i+1, r.URL))
	}
	if len(repos) == 0 {
		sb.WriteString("  (no user repos added)\n")
	}
	sb.WriteString("\nUse <code>delrepo &lt;id&gt;</code> to remove a user repo.")
	return m.edit(ctx, ev, sb.String())
}
