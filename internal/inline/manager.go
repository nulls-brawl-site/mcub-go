// manager.go ports core_inline/lib/manager.py — the InlineManager that
// coordinates the InlineBot and HandlerRegistry into a single high-level API.
//
// SPDX-License-Identifier: MIT
package inline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

// Manager is the high-level inline functionality coordinator.
// It wires the InlineBot, HandlerRegistry, and provides utility methods for
// modules to create callback buttons, inline forms, and send inline results.
type Manager struct {
	bot      *InlineBot
	registry *HandlerRegistry
	kernel   interface{}
	mu       sync.RWMutex
}

// NewManager creates a Manager bound to kernel. Call SetBot once the InlineBot
// has been created and started.
func NewManager(kernel interface{}) *Manager {
	return &Manager{
		registry: NewHandlerRegistry(),
		kernel:   kernel,
	}
}

// SetBot sets the InlineBot. The manager forwards all handler registrations to
// the bot's HandlerRegistry automatically.
func (m *Manager) SetBot(bot *InlineBot) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bot = bot
	// Use the bot's own registry so that handlers registered on the Manager
	// are visible through the bot's update dispatcher.
	if bot != nil {
		m.registry = bot.handlers
	}
}

// Bot returns the active InlineBot (may be nil before SetBot is called).
func (m *Manager) Bot() *InlineBot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.bot
}

// RegisterInlineHandler registers an inline query handler for queries whose
// full text matches pattern (a regexp string). module is used for diagnostics.
func (m *Manager) RegisterInlineHandler(pattern, module string, h InlineQueryHandler) {
	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	reg.RegisterInlineHandler(pattern, module, h)
}

// RegisterCallback stores a callback handler under token. After ttlSecs
// seconds the entry expires and GetCallback will return false. When allowAll
// is true any user may trigger it; otherwise only those in allowedUsers.
func (m *Manager) RegisterCallback(token string, handler CallbackHandler, ttlSecs int, allowAll bool, allowedUsers []int64) {
	entry := CallbackEntry{
		Handler:      handler,
		AllowAll:     allowAll,
		AllowedUsers: allowedUsers,
	}
	if ttlSecs > 0 {
		entry.ExpiresAt = time.Now().Add(time.Duration(ttlSecs) * time.Second)
	}

	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	reg.RegisterCallback(token, entry)
}

// GenerateCallbackToken creates a new cryptographically random UUID-like token
// using crypto/rand.
func (m *Manager) GenerateCallbackToken() string {
	return newToken()
}

// newToken generates a 16-byte random token encoded as a 32-character hex string.
// It falls back to a timestamp-derived string on rand failure (should not happen).
func newToken() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// Extremely unlikely fallback.
		return fmt.Sprintf("%016x%016x", time.Now().UnixNano(), time.Now().UnixMicro())
	}
	// Format as UUID v4 (RFC 4122).
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // variant bits
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(buf[0:4]),
		hex.EncodeToString(buf[4:6]),
		hex.EncodeToString(buf[6:8]),
		hex.EncodeToString(buf[8:10]),
		hex.EncodeToString(buf[10:16]),
	)
}

// InlineQueryAndClick performs an inline query to the bot from the userbot
// side and sends ("clicks") the first result into chatID. Returns (true,
// message, nil) on success. Returns (false, nil, err) on failure.
//
// The userbot client from kernel must implement kernelInfo; otherwise the
// operation is a no-op and returns false.
func (m *Manager) InlineQueryAndClick(ctx context.Context, chatID int64, query string) (bool, interface{}, error) {
	m.mu.RLock()
	bot := m.bot
	m.mu.RUnlock()

	if bot == nil {
		return false, nil, fmt.Errorf("inline: bot not configured")
	}

	ki, ok := m.kernel.(kernelInfo)
	if !ok {
		return false, nil, fmt.Errorf("inline: kernel does not provide client")
	}

	userbot := ki.GetClient()
	if userbot == nil {
		return false, nil, fmt.Errorf("inline: userbot client not available")
	}

	botUser := bot.BotInputUser()
	if botUser == nil {
		return false, nil, fmt.Errorf("inline: bot user ID not known (call GetMe first)")
	}

	// Use the userbot's raw TL API for the inline query.
	api := userbot.API()
	peer := chatIDToInputPeer(chatID)

	results, err := api.MessagesGetInlineBotResults(ctx, &tg.MessagesGetInlineBotResultsRequest{
		Bot:    botUser,
		Peer:   peer,
		Query:  query,
		Offset: "",
	})
	if err != nil {
		return false, nil, fmt.Errorf("inline: get inline bot results: %w", err)
	}
	if len(results.Results) == 0 {
		return false, nil, nil
	}

	firstID := results.Results[0].GetID()
	updates, err := api.MessagesSendInlineBotResult(ctx, &tg.MessagesSendInlineBotResultRequest{
		Peer:     peer,
		QueryID:  results.QueryID,
		ID:       firstID,
		RandomID: randInt63(),
	})
	if err != nil {
		return false, nil, fmt.Errorf("inline: send inline bot result: %w", err)
	}

	msg := extractMessage(updates)
	return true, msg, nil
}

// SendInline queries the bot for results matching query and sends the first
// result to chatID via the userbot. buttons may be used to override the
// keyboard markup on the sent message (not currently wired; preserved for API
// compatibility with the Python source).
func (m *Manager) SendInline(ctx context.Context, chatID int64, query string, buttons interface{}) (bool, error) {
	ok, _, err := m.InlineQueryAndClick(ctx, chatID, query)
	return ok, err
}

// InlineForm creates and sends a form via the inline bot. title is shown as
// the article title, fields describes the form structure, buttons is an optional
// keyboard, autoSend causes the form to be immediately sent if true, and ttl
// is the callback TTL in seconds. Returns (sent, message, error).
func (m *Manager) InlineForm(
	ctx context.Context,
	chatID int64,
	title string,
	fields []map[string]interface{},
	buttons interface{},
	autoSend bool,
	ttl int,
) (bool, interface{}, error) {
	m.mu.RLock()
	bot := m.bot
	m.mu.RUnlock()

	if bot == nil {
		return false, nil, fmt.Errorf("inline: bot not configured")
	}
	if !bot.IsRunning() {
		return false, nil, fmt.Errorf("inline: bot not running")
	}

	// Build the form text from the title + field definitions.
	text := buildFormText(title, fields)

	if !autoSend {
		return false, text, nil
	}

	msg, err := bot.SendMessage(ctx, chatID, text)
	if err != nil {
		return false, nil, err
	}
	return true, msg, nil
}

// MakeCallbackButton creates an inline keyboard button that, when pressed,
// triggers handler. A new token is generated automatically. style controls
// optional visual styling (API-compatible with Python; not used in the MTProto
// layer). ttl is the handler TTL in seconds.
func (m *Manager) MakeCallbackButton(text string, handler CallbackHandler, ttl int, style string) interface{} {
	token := m.GenerateCallbackToken()
	m.RegisterCallback(token, handler, ttl, false, nil)
	return MakeCallbackButton(text, token, style, 0)
}

// IsRunning returns true when the inline bot is active and running.
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	bot := m.bot
	m.mu.RUnlock()
	return bot != nil && bot.IsRunning()
}

// Cleanup removes expired callback entries from the registry.
func (m *Manager) Cleanup() int {
	m.mu.RLock()
	reg := m.registry
	m.mu.RUnlock()
	return reg.Cleanup()
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// buildFormText assembles a simple plain-text form description.
func buildFormText(title string, fields []map[string]interface{}) string {
	if len(fields) == 0 {
		return title
	}
	out := title + "\n"
	for _, f := range fields {
		name, _ := f["name"].(string)
		label, _ := f["label"].(string)
		if label == "" {
			label = name
		}
		out += fmt.Sprintf("• %s\n", label)
	}
	return out
}

// randInt63 returns a pseudo-random int64 using crypto/rand so that it is safe
// to use as a Telegram random_id without seeding concerns.
func randInt63() int64 {
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	var v int64
	for i, b := range buf {
		v |= int64(b) << (i * 8)
	}
	if v < 0 {
		v = -v
	}
	return v
}
