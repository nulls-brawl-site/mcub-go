package modules

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// backupModule provides .backup, .restore, .restore_with commands.
type backupModule struct {
	k *kernel.Kernel
}

func newBackupModule() loader.Module { return &backupModule{} }

// Name implements loader.Module.
func (m *backupModule) Name() string { return "backup" }

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
	return nil
}

// OnUnload implements loader.Module.
func (m *backupModule) OnUnload(k interface{}) error {
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
		{Name: "backup", Description: "create and send a backup zip", Handler: m.cmdBackup},
		{Name: "restore", Description: "<reply> — restore from backup zip", Handler: m.cmdRestore},
		{Name: "restore_with", Description: "<reply> <password> — restore encrypted backup", Handler: m.cmdRestoreWith},
	}
}

// parseArgs strips the prefix+command and returns remaining tokens.
func (m *backupModule) parseArgs(ev *events.NewMessage) []string {
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

// getBackupChatID retrieves the configured backup chat ID from module config.
func (m *backupModule) getBackupChatID() (int64, bool) {
	if m.k == nil || m.k.Config == nil {
		return 0, false
	}
	cfg, err := m.k.Config.GetModuleConfig(m.Name(), nil)
	if err != nil || cfg == nil {
		return 0, false
	}
	switch v := cfg["backup_chat_id"].(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	}
	return 0, false
}

// ---------- createBackup ----------

// createBackup creates a zip archive of config.json, mcub.db, and modules_loaded/.
// Returns the path to the temp zip file (caller must remove it).
func (m *backupModule) createBackup(ctx context.Context) (string, error) {
	_ = ctx
	zipPath := fmt.Sprintf("/tmp/mcub_backup_%d.zip", time.Now().Unix())

	f, err := os.Create(zipPath)
	if err != nil {
		return "", fmt.Errorf("create backup zip: %w", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	// Helper: add a single file to the archive.
	addFile := func(srcPath, archName string) error {
		if _, err := os.Stat(srcPath); os.IsNotExist(err) {
			return nil // skip missing files silently
		}
		r, err := os.Open(srcPath)
		if err != nil {
			return fmt.Errorf("open %s: %w", srcPath, err)
		}
		defer r.Close()

		wr, err := w.Create(archName)
		if err != nil {
			return fmt.Errorf("zip create %s: %w", archName, err)
		}
		_, err = io.Copy(wr, r)
		return err
	}

	// Add config.json.
	if err := addFile("config.json", "config.json"); err != nil {
		return "", err
	}

	// Add mcub.db.
	if err := addFile("mcub.db", "mcub.db"); err != nil {
		return "", err
	}

	// Add modules_loaded/ directory recursively.
	modDir := "modules_loaded"
	if info, err := os.Stat(modDir); err == nil && info.IsDir() {
		err = filepath.WalkDir(modDir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			archName := filepath.ToSlash(path)
			r, err := os.Open(path)
			if err != nil {
				return err
			}
			defer r.Close()
			wr, err := w.Create(archName)
			if err != nil {
				return err
			}
			_, err = io.Copy(wr, r)
			return err
		})
		if err != nil {
			return "", fmt.Errorf("walk modules_loaded: %w", err)
		}
	}

	return zipPath, nil
}

// ---------- .backup ----------

// cmdBackup creates a zip backup and sends it to the backup chat or the current chat.
func (m *backupModule) cmdBackup(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		"📦 <b>Creating backup...</b>"); err != nil {
		return err
	}

	zipPath, err := m.createBackup(ctx)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ <b>Backup failed:</b> %v", err))
	}
	defer os.Remove(zipPath)

	// Decide target chat.
	targetChat := ev.PeerID
	if id, ok := m.getBackupChatID(); ok {
		targetChat = id
	}

	caption := fmt.Sprintf("📦 <b>MCUB Backup</b>\n%s",
		time.Now().Format("2006-01-02 15:04:05"))

	if m.k.Client != nil {
		if _, sendErr := m.k.Client.SendDocument(ctx, targetChat, zipPath, caption); sendErr != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("❌ <b>Backup send failed:</b> %v", sendErr))
		}
	}

	msg := "✅ <b>Backup created and sent.</b>"
	if targetChat != ev.PeerID {
		msg += fmt.Sprintf("\nSent to chat <code>%d</code>.", targetChat)
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, msg)
}

// ---------- .restore ----------

// cmdRestore restores from a backup zip replied to.
func (m *backupModule) cmdRestore(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	if !ev.IsReply {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ Reply to a backup zip message, then run <code>restore</code>.")
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		"⏳ <b>Restoring backup...</b>"); err != nil {
		return err
	}

	return m.restoreFromReply(ctx, ev)
}

// ---------- .restore_with ----------

// cmdRestoreWith restores an encrypted backup with a password.
func (m *backupModule) cmdRestoreWith(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	args := m.parseArgs(ev)
	if len(args) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ Usage: <code>restore_with &lt;password&gt;</code> (reply to backup)")
	}
	// Password not used in this simplified port (encryption not implemented).
	_ = args[0]

	if !ev.IsReply {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ Reply to a backup zip message.")
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		"⏳ <b>Restoring backup...</b>"); err != nil {
		return err
	}

	return m.restoreFromReply(ctx, ev)
}

// restoreFromReply downloads a zip from the replied message and extracts it.
func (m *backupModule) restoreFromReply(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ Client not available.")
	}

	// Download the zip to a temp file using ChatID + ReplyToMsgID.
	tmpPath := fmt.Sprintf("/tmp/mcub_restore_%d.zip", time.Now().Unix())
	defer os.Remove(tmpPath)

	_, err := m.k.Client.DownloadMedia(ctx, mcubclient.DownloadMediaParams{
		ChatID:    ev.PeerID,
		MessageID: ev.ReplyToMsgID,
		FilePath:  tmpPath,
	})
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Download failed: %v", err))
	}

	// Extract zip.
	restored, err := m.extractBackup(tmpPath)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Extraction failed: %v", err))
	}

	if len(restored) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"⚠️ No files were restored.")
	}

	var sb strings.Builder
	sb.WriteString("✅ <b>Restored:</b>\n")
	for _, f := range restored {
		fmt.Fprintf(&sb, "• <code>%s</code>\n", f)
	}
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String()); err != nil {
		return err
	}

	// Trigger restart.
	if m.k.Client != nil {
		_, _ = m.k.Client.EditMessage(ctx, mcubclient.EditMessageParams{
			PeerID:    ev.PeerID,
			MessageID: ev.Raw.ID,
			Text:      "✅ Restored. Restarting...",
		})
	}
	return m.k.Restart()
}

// extractBackup extracts a backup zip to the current working directory.
func (m *backupModule) extractBackup(zipPath string) ([]string, error) {
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
		// Create parent dirs.
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			return restored, fmt.Errorf("mkdir %s: %w", filepath.Dir(name), err)
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
