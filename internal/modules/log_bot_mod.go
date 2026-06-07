package modules

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// logBotModule provides .log_setup and .logs commands.
type logBotModule struct {
	k *kernel.Kernel
}

func newLogBotModule() loader.Module { return &logBotModule{} }

// Name implements loader.Module.
func (m *logBotModule) Name() string { return "log_bot" }

// OnLoad implements loader.Module.
func (m *logBotModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("log_bot: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *logBotModule) OnUnload(k interface{}) error {
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
func (m *logBotModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "log_setup", Description: "[chat_id|off] — configure log chat", Handler: m.cmdLogSetup},
		{Name: "log_entries", Description: "[level] [--tail N] — show recent log entries", Handler: m.cmdLogEntries},
	}
}

// parseArgs strips the prefix+command and returns remaining tokens.
func (m *logBotModule) parseArgs(ev *events.NewMessage) []string {
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

// dbKey for log chat ID.
const logBotChatIDKey = "log_bot:chat_id"

// getLogChatID retrieves the stored log chat ID from DB.
func (m *logBotModule) getLogChatID() (int64, bool) {
	if m.k == nil || m.k.DB == nil {
		// Fall back to config.
		if m.k != nil && m.k.Config != nil && m.k.Config.LogChatID != nil {
			return *m.k.Config.LogChatID, true
		}
		return 0, false
	}
	val, ok, err := m.k.DB.Get(logBotChatIDKey)
	if err != nil || !ok {
		// Fall back to config.
		if m.k.Config != nil && m.k.Config.LogChatID != nil {
			return *m.k.Config.LogChatID, true
		}
		return 0, false
	}
	var id int64
	if _, err := fmt.Sscanf(val, "%d", &id); err == nil {
		return id, true
	}
	return 0, false
}

// setLogChatID stores the log chat ID in DB.
func (m *logBotModule) setLogChatID(id int64) error {
	if m.k == nil || m.k.DB == nil {
		return fmt.Errorf("database not available")
	}
	return m.k.DB.Set(logBotChatIDKey, fmt.Sprintf("%d", id))
}

// clearLogChatID removes the log chat ID from DB.
func (m *logBotModule) clearLogChatID() error {
	if m.k == nil || m.k.DB == nil {
		return fmt.Errorf("database not available")
	}
	return m.k.DB.Delete(logBotChatIDKey)
}

// ---------- .log_setup ----------

// cmdLogSetup configures the log chat.
// .log_setup         → show current log chat ID
// .log_setup <id>    → set log chat
// .log_setup off     → disable logging
func (m *logBotModule) cmdLogSetup(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	if len(args) == 0 {
		chatID, ok := m.getLogChatID()
		if !ok {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"📋 <b>Log setup</b>\n\nLog chat: <i>not configured</i>\n\n"+
					"Use <code>log_setup &lt;chat_id&gt;</code> to set a log chat.\n"+
					"Use <code>log_setup off</code> to disable.")
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("📋 <b>Log setup</b>\n\nLog chat ID: <code>%d</code>\n\n"+
				"Use <code>log_setup off</code> to disable.", chatID))
	}

	arg := strings.TrimSpace(args[0])

	if strings.ToLower(arg) == "off" {
		if err := m.clearLogChatID(); err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("❌ Error disabling log: %v", err))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"✅ <b>Log chat disabled.</b>")
	}

	var chatID int64
	if _, err := fmt.Sscanf(arg, "%d", &chatID); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ Invalid chat ID. Usage: <code>log_setup &lt;chat_id&gt;</code> or <code>log_setup off</code>")
	}

	if err := m.setLogChatID(chatID); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error saving log chat: %v", err))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("✅ <b>Log chat set</b> to <code>%d</code>", chatID))
}

// ---------- .log_entries ----------

// kernelLogFile is the default path to the kernel log.
const kernelLogFile = "logs/kernel.log"

// cmdLogEntries shows recent log entries inline.
// .log_entries [level] [--tail N]
func (m *logBotModule) cmdLogEntries(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	// Defaults.
	level := ""
	n := 50

	// Parse flags.
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--tail" && i+1 < len(args) {
			if _, err := fmt.Sscanf(args[i+1], "%d", &n); err == nil {
				i++
			}
			continue
		}
		if _, ok := validLogLevels[strings.ToLower(a)]; ok {
			level = strings.ToUpper(a)
		}
	}

	logPath := filepath.Clean(kernelLogFile)
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"📋 <b>Logs</b>\n\nLog file not found: <code>"+logPath+"</code>")
	}

	lines, err := m.readLogLines(logPath, level, n)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ Error reading log: %v", err))
	}
	if len(lines) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"📋 <b>Logs</b>\n\n<i>No log entries found.</i>")
	}

	levelLabel := ""
	if level != "" {
		levelLabel = " [" + level + "]"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "📋 <b>Recent logs%s</b> (last %d lines):\n<blockquote expandable>\n", levelLabel, len(lines))
	for _, line := range lines {
		fmt.Fprintf(&sb, "<code>%s</code>\n", htmlEscapeLogLine(line))
	}
	sb.WriteString("</blockquote>")
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// validLogLevels for filtering.
var validLogLevels = map[string]struct{}{
	"debug": {}, "info": {}, "warning": {}, "warn": {},
	"error": {}, "critical": {},
}

// readLogLines reads the last n lines from logPath, optionally filtered by level.
func (m *logBotModule) readLogLines(logPath, level string, n int) ([]string, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var all []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if level == "" || strings.Contains(line, "["+level+"]") || strings.Contains(line, " "+level+" ") {
			all = append(all, line)
		}
	}
	if sc.Err() != nil {
		return nil, sc.Err()
	}

	// Return last n lines.
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, nil
}

// htmlEscapeLogLine escapes < > & for safe HTML display.
func htmlEscapeLogLine(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
