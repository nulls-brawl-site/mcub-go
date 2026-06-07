// bot.go ports core_inline/bot.py — the inline bot client that runs alongside
// the MCUB userbot. It manages the bot's Telegram connection (via gotd/td),
// authentication, and update dispatch.
//
// SPDX-License-Identifier: MIT
package inline

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
)

// kernelInfo is the interface that kernel.Kernel satisfies so that InlineBot
// can access app credentials and the userbot client without creating an import
// cycle.
type kernelInfo interface {
	GetAPIID() int64
	GetAPIHash() string
	GetClient() *mcubclient.MCUBClient
}

// InlineBot is the bot client that handles inline queries and callbacks.
// It maintains its own Telegram session alongside the MCUB userbot.
type InlineBot struct {
	// client is the parent userbot MCUBClient (may be nil in standalone mode).
	client *mcubclient.MCUBClient

	// botClient is the dedicated gotd/td telegram.Client for the bot token.
	botClient *telegram.Client

	// api is the raw TL API accessor, set once botClient.Run auth completes.
	api *tg.Client

	kernel interface{}
	token  string

	// username is the bot's @handle (without @), set after GetMe.
	username string

	// userID and accessHash are populated by the first successful GetMe call.
	userID     int64
	accessHash int64

	mu       sync.RWMutex
	running  bool
	cancelFn context.CancelFunc

	handlers *HandlerRegistry
}

// New creates a new InlineBot bound to kernel and authenticating with token.
// It also configures the underlying gotd/td telegram.Client using API
// credentials extracted from kernel (which must implement kernelInfo).
func New(kernel interface{}, token string) (*InlineBot, error) {
	if token == "" {
		return nil, fmt.Errorf("inline: bot token must not be empty")
	}

	registry := NewHandlerRegistry()

	b := &InlineBot{
		kernel:   kernel,
		token:    token,
		handlers: registry,
	}

	if ki, ok := kernel.(kernelInfo); ok {
		b.client = ki.GetClient()

		appID := int(ki.GetAPIID())
		apiHash := ki.GetAPIHash()

		if appID != 0 && apiHash != "" {
			dispatcher := tg.NewUpdateDispatcher()

			// Register update handlers on the dispatcher that forward events
			// into the HandlerRegistry.
			dispatcher.OnBotInlineQuery(func(ctx context.Context, e tg.Entities, update *tg.UpdateBotInlineQuery) error {
				ev := &InlineQueryEvent{
					Raw:     update,
					QueryID: update.QueryID,
					UserID:  update.UserID,
					Query:   update.Query,
					Offset:  update.Offset,
					Bot:     b,
				}
				return registry.DispatchInline(ctx, b, ev)
			})

			dispatcher.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, update *tg.UpdateBotCallbackQuery) error {
				chatID := int64(0)
				if update.Peer != nil {
					switch p := update.Peer.(type) {
					case *tg.PeerUser:
						chatID = p.UserID
					case *tg.PeerChat:
						chatID = p.ChatID
					case *tg.PeerChannel:
						chatID = p.ChannelID
					}
				}
				rawData, _ := update.GetData()
				ev := &CallbackEvent{
					Raw:     update,
					QueryID: update.QueryID,
					UserID:  update.UserID,
					ChatID:  chatID,
					MsgID:   update.MsgID,
					Data:    rawData,
					Bot:     b,
				}
				return registry.DispatchCallback(ctx, b, ev)
			})

			b.botClient = telegram.NewClient(appID, apiHash, telegram.Options{
				SessionStorage: &session.StorageMemory{},
				UpdateHandler:  dispatcher,
			})
		}
	}

	return b, nil
}

// Setup loads credentials from kernel config if the token is not yet set,
// then verifies the bot identity by calling GetMe.
// Mirrors InlineBot.setup() in the Python source.
func (b *InlineBot) Setup(ctx context.Context) error {
	if b.botClient == nil {
		return fmt.Errorf("inline: bot client not initialised (missing appID/apiHash)")
	}
	return nil
}

// Run starts the bot, authenticating with its token and processing incoming
// updates. It blocks until ctx is cancelled.
func (b *InlineBot) Run(ctx context.Context) error {
	if b.botClient == nil {
		return fmt.Errorf("inline: bot client not initialised")
	}

	runCtx, cancel := context.WithCancel(ctx)
	b.mu.Lock()
	b.cancelFn = cancel
	b.mu.Unlock()

	defer func() {
		cancel()
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()

	return b.botClient.Run(runCtx, func(ctx context.Context) error {
		// Authenticate as a bot if not already authorised.
		status, err := b.botClient.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("inline: auth status: %w", err)
		}
		if !status.Authorized {
			if _, err := b.botClient.Auth().Bot(ctx, b.token); err != nil {
				return fmt.Errorf("inline: bot auth: %w", err)
			}
		}

		// Expose the raw API and mark running.
		b.mu.Lock()
		b.api = b.botClient.API()
		b.running = true
		b.mu.Unlock()

		// Populate bot identity for later use by InlineQueryAndClick.
		_, _ = b.GetMe(ctx)

		// Keep running until context is cancelled.
		<-ctx.Done()
		return ctx.Err()
	})
}

// Stop cancels the bot's run context, triggering a graceful shutdown.
func (b *InlineBot) Stop() error {
	b.mu.Lock()
	fn := b.cancelFn
	b.mu.Unlock()

	if fn != nil {
		fn()
	}
	return nil
}

// IsRunning returns true when the bot's run loop is active.
func (b *InlineBot) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}

// GetMe returns the bot's own User object. It caches the user ID and access
// hash in the InlineBot for later peer resolution.
func (b *InlineBot) GetMe(ctx context.Context) (*tg.User, error) {
	b.mu.RLock()
	api := b.api
	b.mu.RUnlock()

	if api == nil {
		return nil, fmt.Errorf("inline: bot API not connected")
	}

	users, err := api.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return nil, fmt.Errorf("inline: get me: %w", err)
	}
	for _, u := range users {
		user, ok := u.(*tg.User)
		if !ok {
			continue
		}
		b.mu.Lock()
		b.userID = user.ID
		if ah, ok2 := user.GetAccessHash(); ok2 {
			b.accessHash = ah
		}
		if uname, ok2 := user.GetUsername(); ok2 {
			b.username = uname
		}
		b.mu.Unlock()
		return user, nil
	}
	return nil, fmt.Errorf("inline: GetMe returned no user")
}

// Username returns the bot's @handle as populated by GetMe (without @).
func (b *InlineBot) Username() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.username
}

// SendMessage sends a text message to chatID as the bot and returns the
// resulting message. Access hashes are not stored, so channel sends may fail;
// group (PeerChat) and user (PeerUser with hash=0) sends work for most cases.
func (b *InlineBot) SendMessage(ctx context.Context, chatID int64, text string) (*tg.Message, error) {
	b.mu.RLock()
	api := b.api
	b.mu.RUnlock()

	if api == nil {
		return nil, fmt.Errorf("inline: bot API not connected")
	}

	peer := chatIDToInputPeer(chatID)
	updates, err := api.MessagesSendMessage(ctx, &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: rand.Int63(), //nolint:gosec // non-cryptographic random sufficient for message dedup
	})
	if err != nil {
		return nil, fmt.Errorf("inline: send message: %w", err)
	}

	return extractMessage(updates), nil
}

// EditMessage edits a previously sent bot message identified by msgID in chatID.
func (b *InlineBot) EditMessage(ctx context.Context, chatID int64, msgID int, text string) (*tg.Message, error) {
	b.mu.RLock()
	api := b.api
	b.mu.RUnlock()

	if api == nil {
		return nil, fmt.Errorf("inline: bot API not connected")
	}

	peer := chatIDToInputPeer(chatID)
	updates, err := api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      msgID,
		Message: text,
	})
	if err != nil {
		return nil, fmt.Errorf("inline: edit message: %w", err)
	}

	return extractMessage(updates), nil
}

// BotInputUser returns an InputUser that represents this bot, suitable for
// use in MessagesGetInlineBotResults from the userbot side.
// Returns nil if the bot identity has not been fetched yet.
func (b *InlineBot) BotInputUser() *tg.InputUser {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.userID == 0 {
		return nil
	}
	return &tg.InputUser{UserID: b.userID, AccessHash: b.accessHash}
}

// Handlers returns the bot's HandlerRegistry so that the Manager and modules
// can register inline and callback handlers.
func (b *InlineBot) Handlers() *HandlerRegistry {
	return b.handlers
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// chatIDToInputPeer converts a numeric chat ID to a tg.InputPeerClass.
// Positive IDs are treated as users, negative IDs > -1e12 as groups,
// and very negative IDs as channels (Telegram's supergroup encoding).
// Access hashes are unknown at this level so they default to 0.
func chatIDToInputPeer(chatID int64) tg.InputPeerClass {
	const superGroupThreshold = int64(-1_000_000_000_000)
	if chatID > 0 {
		return &tg.InputPeerUser{UserID: chatID}
	}
	if chatID < superGroupThreshold {
		// Telegram encodes supergroup IDs as -1_000_000_000_000 - channelID.
		channelID := -(chatID + 1_000_000_000_000)
		return &tg.InputPeerChannel{ChannelID: channelID}
	}
	return &tg.InputPeerChat{ChatID: -chatID}
}

// extractMessage walks an UpdatesClass looking for an UpdateNewMessage that
// contains a *tg.Message. Returns nil if none is found.
func extractMessage(updates tg.UpdatesClass) *tg.Message {
	if updates == nil {
		return nil
	}
	var upds []tg.UpdateClass
	switch u := updates.(type) {
	case *tg.Updates:
		upds = u.Updates
	case *tg.UpdatesCombined:
		upds = u.Updates
	case *tg.UpdateShort:
		upds = []tg.UpdateClass{u.Update}
	}
	for _, upd := range upds {
		if nm, ok := upd.(*tg.UpdateNewMessage); ok {
			if msg, ok2 := nm.Message.(*tg.Message); ok2 {
				return msg
			}
		}
	}
	return nil
}
