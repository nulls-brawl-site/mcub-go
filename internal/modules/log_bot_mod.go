// SPDX-License-Identifier: MIT
// Port of modules/log_bot.py from MCUB-fork.
//
// Python log_bot.py (LogBot):
//   - on_load(): loads config, calls setup_log_chat(), send_startup_message()
//   - log_setup command: runs setup_log_chat() (auto-creates/finds "MCUB-logs" group)
//   - update_check_loop (@loop): polls git, notifies on new commits
//   - on_update_callback: git pull + restart
//   - send_startup_message(): sends branded banner to log chat
//   - Config: banner_url, start_message, placeholders, auto_update
//
// The Go kernel does not yet support:
//   - Automatic Telegram group creation (setup_log_chat)
//   - Background loops (@loop decorator)
//   - Bot client startup messages
//
// The .log_setup command below uses the same langpack strings as Python and
// sets / shows the log chat ID rather than auto-creating a group.

package modules

import (
	"context"
	"fmt"
	"strings"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// logBotModule provides the .log_setup command.
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
// Python log_bot.py has a single @command("log_setup") handler.
func (m *logBotModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "log_setup", Description: "setup logging chat", Handler: m.cmdLogSetup},
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
		if m.k != nil && m.k.Config != nil && m.k.Config.LogChatID != nil {
			return *m.k.Config.LogChatID, true
		}
		return 0, false
	}
	val, ok, err := m.k.DB.Get(logBotChatIDKey)
	if err != nil || !ok {
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
// Python: always runs setup_log_chat() (auto-creates group if needed).
// Go: no args → show setup status; <id> → set; off → disable.
// Messages use the same langpack keys as Python (log_setup_title, log_setup_success, log_setup_fail).
func (m *logBotModule) cmdLogSetup(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	// Announce setup start — mirrors Python: await event.edit(self.lang["log_setup_title"])
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		s(m.k, "log_bot", "log_setup_title")); err != nil {
		return err
	}

	args := m.parseArgs(ev)

	if len(args) == 0 {
		// No args: show current log chat configuration (Go cannot auto-create group).
		chatID, ok := m.getLogChatID()
		if !ok {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				s(m.k, "log_bot", "log_setup_fail"))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s\nID: `%d`", s(m.k, "log_bot", "log_setup_success"), chatID))
	}

	arg := strings.TrimSpace(args[0])

	if strings.ToLower(arg) == "off" {
		if err := m.clearLogChatID(); err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				s(m.k, "log_bot", "log_setup_fail"))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "log_bot", "log_setup_success"))
	}

	var chatID int64
	if _, err := fmt.Sscanf(arg, "%d", &chatID); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "log_bot", "log_setup_fail"))
	}

	if err := m.setLogChatID(chatID); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "log_bot", "log_setup_fail"))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s\nID: `%d`", s(m.k, "log_bot", "log_setup_success"), chatID))
}
