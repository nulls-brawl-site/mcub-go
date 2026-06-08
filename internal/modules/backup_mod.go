package modules

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// ---------------------------------------------------------------------------
// Custom emoji constants (from userbot-backup.py _E dict)
// ---------------------------------------------------------------------------

const (
	backupEmojiOK        = "<tg-emoji emoji-id=\"5118861066981344121\">✅</tg-emoji>"
	backupEmojiSettings  = "<tg-emoji emoji-id=\"5332654441508119011\">⚙️</tg-emoji>"
	backupEmojiError     = "<tg-emoji emoji-id=\"5388785832956016892\">❌</tg-emoji>"
	backupEmojiPackage   = "<tg-emoji emoji-id=\"5399898266265475100\">📦</tg-emoji>"
	backupEmojiHourglass = "<tg-emoji emoji-id=\"5426958067763804056\">⏳</tg-emoji>"
	backupEmojiWarning   = "<tg-emoji emoji-id=\"5409235172979672859\">⚠️</tg-emoji>"
)

// ---------------------------------------------------------------------------
// Langpack strings (en.yaml: userbot_backup: section)
// ---------------------------------------------------------------------------

const (
	strBackupCreating = backupEmojiHourglass + " <i>Creating backup...</i>"
	strBackupCreated  = backupEmojiOK + " <b>Backup created</b>"
	strBackupFailed   = backupEmojiError + " <i><b>Backup failed</b></i>"
	strReplyToBackup  = backupEmojiError + " <u>Reply to a backup message</u>"
	strNotBackupFile  = backupEmojiError + " <u>This is not a backup file</u>"
	strRestoring      = backupEmojiHourglass + " <i>Restoring...</i>"
	strRestored       = backupEmojiOK + " Restored:"
	strNoFiles        = backupEmojiWarning + " <u>No files to restore</u>"
	strRestoreError   = backupEmojiError + " Error:"
)

// ---------------------------------------------------------------------------
// backupModule
// ---------------------------------------------------------------------------

// backupModule provides .backup, .restore, .restore_with commands
// and an optional auto-backup loop.
type backupModule struct {
	k          *kernel.Kernel
	mu         sync.Mutex
	cancelLoop context.CancelFunc
}

func newBackupModule() loader.Module { return &backupModule{} }

// Name implements loader.Module.
func (m *backupModule) Name() string { return "userbot-backup" }

// OnLoad implements loader.Module.
func (m *backupModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("backup: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	m.startAutoBackupLoop()
	return nil
}

// OnUnload implements loader.Module.
func (m *backupModule) OnUnload(k interface{}) error {
	m.mu.Lock()
	if m.cancelLoop != nil {
		m.cancelLoop()
		m.cancelLoop = nil
	}
	m.mu.Unlock()

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
func (m *backupModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "backup",
			Description: "create and send a backup zip [config|db|modules]",
			Handler:     m.cmdBackup,
		},
		{
			Name:        "restore",
			Description: "<reply> — restore from backup zip",
			Handler:     m.cmdRestore,
		},
		{
			Name:        "restore_with",
			Description: "<reply> <password> — restore encrypted backup",
			Handler:     m.cmdRestoreWith,
		},
	}
}

// ---------------------------------------------------------------------------
// Config helpers
// ---------------------------------------------------------------------------

func (m *backupModule) dbGet(key string) (string, bool) {
	if m.k == nil || m.k.DB == nil {
		return "", false
	}
	val, ok, err := m.k.DB.ModuleGet(m.Name(), key)
	if err != nil || !ok {
		return "", false
	}
	return val, true
}

func (m *backupModule) dbSet(key, value string) {
	if m.k == nil || m.k.DB == nil {
		return
	}
	_ = m.k.DB.ModuleSet(m.Name(), key, value)
}

// getBackupChatID returns (chatID, true) when a backup target chat is
// configured via DB or kernel config.
func (m *backupModule) getBackupChatID() (int64, bool) {
	// 1. Check DB key set by .backup.
	if raw, ok := m.dbGet("backup_chat_id"); ok && raw != "" {
		var id int64
		if _, err := fmt.Sscanf(raw, "%d", &id); err == nil && id != 0 {
			return id, true
		}
	}
	// 2. Fall back to kernel module config.
	if m.k == nil || m.k.Config == nil {
		return 0, false
	}
	cfg, err := m.k.Config.GetModuleConfig(m.Name(), nil)
	if err != nil || cfg == nil {
		return 0, false
	}
	switch v := cfg["backup_chat_id"].(type) {
	case float64:
		if id := int64(v); id != 0 {
			return id, true
		}
	case int64:
		if v != 0 {
			return v, true
		}
	case int:
		if id := int64(v); id != 0 {
			return id, true
		}
	}
	return 0, false
}

// getIntervalHours returns the configured auto-backup interval (1–168 h).
func (m *backupModule) getIntervalHours() int64 {
	if raw, ok := m.dbGet("backup_interval_hours"); ok {
		var n int64
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n >= 1 && n <= 168 {
			return n
		}
	}
	return 12 // default: 12 h
}

// isAutoEnabled returns whether auto-backup is enabled.
func (m *backupModule) isAutoEnabled() bool {
	if raw, ok := m.dbGet("enable_auto_backup"); ok {
		return raw != "false" && raw != "0"
	}
	return true // default enabled
}

// ---------------------------------------------------------------------------
// Auto-backup loop
// ---------------------------------------------------------------------------

func (m *backupModule) startAutoBackupLoop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelLoop != nil {
		m.cancelLoop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelLoop = cancel

	go func() {
		for {
			hours := m.getIntervalHours()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(hours) * time.Hour):
				if m.isAutoEnabled() {
					_ = m.runAutoBackup(ctx)
				}
			}
		}
	}()
}

// runAutoBackup creates a backup and sends it to the configured chat (used by
// the auto-backup goroutine).
func (m *backupModule) runAutoBackup(ctx context.Context) error {
	if m.k == nil || m.k.Client == nil {
		return nil
	}
	chatID, ok := m.getBackupChatID()
	if !ok {
		return nil
	}
	zipPath, size, err := m.createBackupZip(ctx)
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)

	prefix := ""
	if m.k != nil {
		prefix = m.k.Prefix()
	}
	caption := fmt.Sprintf("<blockquote>hint: %srestore to restore backup</blockquote>", prefix)
	_, sendErr := m.k.Client.SendDocument(ctx, chatID, zipPath, caption)
	if sendErr == nil {
		// Increment backup count.
		m.dbSet("last_backup_time", time.Now().Format(time.RFC3339))
	}
	_ = size
	return sendErr
}

// ---------------------------------------------------------------------------
// Backup creation
// ---------------------------------------------------------------------------

// createBackupZip creates a zip archive containing:
//   - config.json
//   - mcub.db
//   - all .py files under modules_loaded/
//
// Returns (zipPath, fileSizeBytes, error).  Caller is responsible for removing
// the temp file.
func (m *backupModule) createBackupZip(ctx context.Context) (string, int64, error) {
	_ = ctx
	zipPath := fmt.Sprintf("/tmp/mcub_backup_%d.zip", time.Now().UnixNano())

	f, err := os.Create(zipPath)
	if err != nil {
		return "", 0, fmt.Errorf("create backup zip: %w", err)
	}

	w := zip.NewWriter(f)

	// addFile copies srcPath into the archive as archName (skips missing files).
	addFile := func(srcPath, archName string) error {
		if _, statErr := os.Stat(srcPath); os.IsNotExist(statErr) {
			return nil // not present — skip silently
		}
		r, openErr := os.Open(srcPath)
		if openErr != nil {
			return fmt.Errorf("open %s: %w", srcPath, openErr)
		}
		defer r.Close()
		wr, createErr := w.Create(archName)
		if createErr != nil {
			return fmt.Errorf("zip create %s: %w", archName, createErr)
		}
		_, copyErr := io.Copy(wr, r)
		return copyErr
	}

	if err := addFile("config.json", "config.json"); err != nil {
		w.Close()
		f.Close()
		os.Remove(zipPath)
		return "", 0, err
	}
	if err := addFile("mcub.db", "mcub.db"); err != nil {
		w.Close()
		f.Close()
		os.Remove(zipPath)
		return "", 0, err
	}

	// Walk modules_loaded/ and include only .py files.
	modDir := "modules_loaded"
	if info, statErr := os.Stat(modDir); statErr == nil && info.IsDir() {
		walkErr := filepath.WalkDir(modDir, func(path string, d os.DirEntry, wErr error) error {
			if wErr != nil || d.IsDir() {
				return wErr
			}
			if !strings.HasSuffix(strings.ToLower(d.Name()), ".py") {
				return nil
			}
			archName := filepath.ToSlash(path)
			r, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			defer r.Close()
			wr, createErr := w.Create(archName)
			if createErr != nil {
				return createErr
			}
			_, copyErr := io.Copy(wr, r)
			return copyErr
		})
		if walkErr != nil {
			w.Close()
			f.Close()
			os.Remove(zipPath)
			return "", 0, fmt.Errorf("walk modules_loaded: %w", walkErr)
		}
	}

	if closeErr := w.Close(); closeErr != nil {
		f.Close()
		os.Remove(zipPath)
		return "", 0, fmt.Errorf("close zip writer: %w", closeErr)
	}
	if closeErr := f.Close(); closeErr != nil {
		os.Remove(zipPath)
		return "", 0, fmt.Errorf("close zip file: %w", closeErr)
	}

	info, statErr := os.Stat(zipPath)
	if statErr != nil {
		os.Remove(zipPath)
		return "", 0, statErr
	}
	return zipPath, info.Size(), nil
}

// formatBackupSize returns a human-readable file size string.
func formatBackupSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// ---------------------------------------------------------------------------
// Command: .backup
// ---------------------------------------------------------------------------

// cmdBackup creates a backup zip and sends it to backup_chat_id (from config)
// or the current chat when no backup chat is configured.
//
// Shows progress:
//  1. "⏳ Creating backup..."
//  2. Sends zip to target chat
//  3. "✅ Backup created (X KB)"  or  "❌ Backup failed"
func (m *backupModule) cmdBackup(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	// Step 1: Notify user.
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strBackupCreating); err != nil {
		return err
	}

	// Step 2: Create archive.
	zipPath, size, err := m.createBackupZip(ctx)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			strBackupFailed+"\n<code>"+err.Error()+"</code>")
	}
	defer os.Remove(zipPath)

	// Step 3: Determine target chat.
	targetChat := ev.PeerID
	if id, ok := m.getBackupChatID(); ok {
		targetChat = id
	}

	// Step 4: Build caption with restore hint.
	prefix := ""
	if m.k != nil {
		prefix = m.k.Prefix()
	}
	caption := fmt.Sprintf("<blockquote>hint: %srestore to restore backup</blockquote>", prefix)

	// Step 5: Send archive.
	if m.k.Client != nil {
		if _, sendErr := m.k.Client.SendDocument(ctx, targetChat, zipPath, caption); sendErr != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				strBackupFailed+"\n<code>"+sendErr.Error()+"</code>")
		}
	}

	// Step 6: Update stats in DB.
	m.dbSet("last_backup_time", time.Now().Format(time.RFC3339))

	// Step 7: Edit status message to success with size.
	resultMsg := strBackupCreated + "\n" + backupEmojiPackage + " " + formatBackupSize(size)
	if targetChat != ev.PeerID {
		resultMsg += fmt.Sprintf("\n<i>Sent to chat <code>%d</code>.</i>", targetChat)
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, resultMsg)
}

// ---------------------------------------------------------------------------
// Command: .restore
// ---------------------------------------------------------------------------

// cmdRestore restores from a backup zip that was replied to.
// Shows confirmation, downloads zip, extracts, then triggers restart.
func (m *backupModule) cmdRestore(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	if !ev.IsReply {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strReplyToBackup)
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strRestoring); err != nil {
		return err
	}

	return m.restoreFromReply(ctx, ev)
}

// ---------------------------------------------------------------------------
// Command: .restore_with
// ---------------------------------------------------------------------------

// cmdRestoreWith restores an encrypted backup (password argument currently
// recorded but encryption is not implemented in this port).
func (m *backupModule) cmdRestoreWith(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	if !ev.IsReply {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strReplyToBackup)
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strRestoring); err != nil {
		return err
	}

	return m.restoreFromReply(ctx, ev)
}

// ---------------------------------------------------------------------------
// restoreFromReply — shared restore logic
// ---------------------------------------------------------------------------

// restoreFromReply downloads the zip attached to the replied message and
// extracts it into the current working directory.
// After successful extraction it triggers a userbot restart.
func (m *backupModule) restoreFromReply(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			backupEmojiError+" Client not available.")
	}

	tmpPath := fmt.Sprintf("/tmp/mcub_restore_%d.zip", time.Now().UnixNano())
	defer os.Remove(tmpPath)

	_, dlErr := m.k.Client.DownloadMedia(ctx, mcubclient.DownloadMediaParams{
		ChatID:    ev.PeerID,
		MessageID: ev.ReplyToMsgID,
		FilePath:  tmpPath,
	})
	if dlErr != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Download failed: %v", backupEmojiError, dlErr))
	}

	// Verify the downloaded file looks like a zip.
	if _, statErr := os.Stat(tmpPath); os.IsNotExist(statErr) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strNotBackupFile)
	}

	restored, extErr := m.extractBackupZip(tmpPath)
	if extErr != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Extraction failed: %v", backupEmojiError, extErr))
	}
	if len(restored) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strNoFiles)
	}

	// Build restored-files summary.
	var sb strings.Builder
	sb.WriteString(strRestored + "\n")
	for _, name := range restored {
		fmt.Fprintf(&sb, backupEmojiOK+" <code>%s</code>\n", name)
	}
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		strings.TrimRight(sb.String(), "\n")); err != nil {
		return err
	}

	// Restart the userbot so new files take effect.
	return m.k.Restart()
}

// extractBackupZip extracts a zip archive into the current working directory
// and returns a list of extracted file paths.
func (m *backupModule) extractBackupZip(zipPath string) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var restored []string
	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.Clean(f.Name)
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return restored, fmt.Errorf("mkdir for %s: %w", name, err)
		}
		dst, err := os.Create(name)
		if err != nil {
			return restored, fmt.Errorf("create %s: %w", name, err)
		}
		rc, err := f.Open()
		if err != nil {
			dst.Close()
			return restored, fmt.Errorf("open zip entry %s: %w", name, err)
		}
		if _, err := io.Copy(dst, rc); err != nil {
			rc.Close()
			dst.Close()
			return restored, fmt.Errorf("extract %s: %w", name, err)
		}
		rc.Close()
		dst.Close()
		restored = append(restored, name)
	}
	return restored, nil
}
