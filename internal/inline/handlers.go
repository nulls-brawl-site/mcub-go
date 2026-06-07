// handlers.go ports core_inline/handlers.py — inline query and callback
// handling including the handler registry, event wrappers, and dispatch logic.
//
// SPDX-License-Identifier: MIT
package inline

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/gotd/td/tg"
)

// InlineQueryHandler handles an inline query sent to the bot.
type InlineQueryHandler func(ctx context.Context, query *InlineQueryEvent) error

// CallbackHandler handles a callback triggered by an inline button press.
type CallbackHandler func(ctx context.Context, call *CallbackEvent) error

// InlineQueryEvent wraps a tg.UpdateBotInlineQuery with helper methods.
type InlineQueryEvent struct {
	// Raw is the underlying Telegram update.
	Raw *tg.UpdateBotInlineQuery

	QueryID int64
	UserID  int64
	Query   string
	Offset  string

	// Bot is the InlineBot that received this event.
	Bot *InlineBot
}

// AnswerWithArticles sends article results to the inline query.
// articles is the list of results to show; cacheTime controls how long
// Telegram may cache the result list for this user/query combination.
func (e *InlineQueryEvent) AnswerWithArticles(ctx context.Context, articles []*ArticleResult, cacheTime int) error {
	e.Bot.mu.RLock()
	api := e.Bot.api
	e.Bot.mu.RUnlock()

	if api == nil {
		return fmt.Errorf("inline: bot API not connected")
	}

	results := make([]tg.InputBotInlineResultClass, 0, len(articles))
	for _, a := range articles {
		msgContent := &tg.InputBotInlineMessageText{
			Message: a.Text,
		}

		// Attach keyboard if provided.
		if a.Buttons != nil {
			if kb, ok := a.Buttons.(tg.ReplyMarkupClass); ok {
				msgContent.SetReplyMarkup(kb)
			}
		}

		res := &tg.InputBotInlineResult{
			ID:          a.ID,
			Type:        "article",
			SendMessage: msgContent,
		}
		if a.Title != "" {
			res.SetTitle(a.Title)
		}
		if a.Description != "" {
			res.SetDescription(a.Description)
		}
		if a.URL != "" {
			res.SetURL(a.URL)
		}
		results = append(results, res)
	}

	req := &tg.MessagesSetInlineBotResultsRequest{
		QueryID:   e.QueryID,
		Results:   results,
		CacheTime: cacheTime,
	}
	_, err := api.MessagesSetInlineBotResults(ctx, req)
	if err != nil {
		return fmt.Errorf("inline: answer inline query: %w", err)
	}
	return nil
}

// CallbackEvent wraps a tg.UpdateBotCallbackQuery with helper methods.
type CallbackEvent struct {
	// Raw is the underlying Telegram update.
	Raw *tg.UpdateBotCallbackQuery

	QueryID int64
	UserID  int64
	// ChatID is derived from Raw.Peer; may be 0 if peer is unknown.
	ChatID int64
	MsgID  int
	Data   []byte

	// Bot is the InlineBot that received this event.
	Bot *InlineBot
}

// Answer answers the callback query, optionally showing an alert popup.
func (e *CallbackEvent) Answer(ctx context.Context, text string, alert bool) error {
	e.Bot.mu.RLock()
	api := e.Bot.api
	e.Bot.mu.RUnlock()

	if api == nil {
		return fmt.Errorf("inline: bot API not connected")
	}

	req := &tg.MessagesSetBotCallbackAnswerRequest{
		QueryID:   e.QueryID,
		CacheTime: 0,
	}
	req.SetAlert(alert)
	if text != "" {
		req.SetMessage(text)
	}

	_, err := api.MessagesSetBotCallbackAnswer(ctx, req)
	if err != nil {
		return fmt.Errorf("inline: answer callback: %w", err)
	}
	return nil
}

// Edit edits the message that contained the pressed button.
// buttons, if non-nil, must be a tg.ReplyMarkupClass to update the keyboard.
func (e *CallbackEvent) Edit(ctx context.Context, text string, buttons interface{}) error {
	e.Bot.mu.RLock()
	api := e.Bot.api
	e.Bot.mu.RUnlock()

	if api == nil {
		return fmt.Errorf("inline: bot API not connected")
	}

	peer, err := peerFromRaw(e.Raw.Peer)
	if err != nil {
		return fmt.Errorf("inline: edit message: %w", err)
	}

	req := &tg.MessagesEditMessageRequest{
		Peer:    peer,
		ID:      e.MsgID,
		Message: text,
	}
	if buttons != nil {
		if kb, ok := buttons.(tg.ReplyMarkupClass); ok {
			req.SetReplyMarkup(kb)
		}
	}

	_, err = api.MessagesEditMessage(ctx, req)
	if err != nil {
		return fmt.Errorf("inline: edit message: %w", err)
	}
	return nil
}

// ArticleResult is a text article shown as an inline query result.
type ArticleResult struct {
	// ID uniquely identifies this result within the answer.
	ID string
	// Title is the result heading shown in the inline picker.
	Title string
	// Description is the subtitle shown below the title.
	Description string
	// Text is the message content sent when the result is selected.
	Text string
	// ParseMode controls text formatting (e.g. "html", "markdown").
	ParseMode string
	// URL is an optional web URL shown with the result.
	URL string
	// ThumbURL is an optional thumbnail image URL.
	ThumbURL string
	// Buttons is an optional reply markup (tg.ReplyMarkupClass or nil).
	Buttons interface{}
}

// inlineSubscription holds a registered pattern and its handler.
type inlineSubscription struct {
	pattern *regexp.Regexp
	handler InlineQueryHandler
	module  string
}

// CallbackEntry stores a single registered callback handler with expiry
// and access-control metadata.
type CallbackEntry struct {
	Handler      CallbackHandler
	ExpiresAt    time.Time
	AllowAll     bool
	AllowedUsers []int64
}

// isExpired reports whether the entry has passed its TTL.
func (e CallbackEntry) isExpired() bool {
	return !e.ExpiresAt.IsZero() && time.Now().After(e.ExpiresAt)
}

// isAllowed reports whether userID may trigger this callback.
func (e CallbackEntry) isAllowed(userID int64) bool {
	if e.AllowAll {
		return true
	}
	for _, id := range e.AllowedUsers {
		if id == userID {
			return true
		}
	}
	return false
}

// HandlerRegistry stores and dispatches inline query and callback handlers.
// It is safe for concurrent use.
type HandlerRegistry struct {
	mu               sync.RWMutex
	inlineHandlers   []inlineSubscription
	callbackHandlers map[string]CallbackEntry
}

// NewHandlerRegistry creates an empty HandlerRegistry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		callbackHandlers: make(map[string]CallbackEntry),
	}
}

// RegisterInlineHandler registers an inline query handler for queries matching
// pattern (a regular expression string). module is used for diagnostics.
func (r *HandlerRegistry) RegisterInlineHandler(pattern, module string, h InlineQueryHandler) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = regexp.MustCompile(regexp.QuoteMeta(pattern))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inlineHandlers = append(r.inlineHandlers, inlineSubscription{
		pattern: re,
		handler: h,
		module:  module,
	})
}

// RegisterCallback registers a callback handler under token.
func (r *HandlerRegistry) RegisterCallback(token string, entry CallbackEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callbackHandlers[token] = entry
}

// GetCallback returns the handler for token if it exists, has not expired, and
// is accessible by userID. The second return value reports whether a valid
// entry was found.
func (r *HandlerRegistry) GetCallback(token string, userID int64) (CallbackHandler, bool) {
	r.mu.RLock()
	entry, ok := r.callbackHandlers[token]
	r.mu.RUnlock()

	if !ok {
		return nil, false
	}
	if entry.isExpired() {
		r.mu.Lock()
		delete(r.callbackHandlers, token)
		r.mu.Unlock()
		return nil, false
	}
	if !entry.isAllowed(userID) {
		return nil, false
	}
	return entry.Handler, true
}

// DispatchInline routes an inline query to the first matching handler.
// Returns the first non-nil error; returns nil when no handler matches.
func (r *HandlerRegistry) DispatchInline(ctx context.Context, bot *InlineBot, ev *InlineQueryEvent) error {
	r.mu.RLock()
	handlers := make([]inlineSubscription, len(r.inlineHandlers))
	copy(handlers, r.inlineHandlers)
	r.mu.RUnlock()

	for _, sub := range handlers {
		if sub.pattern.MatchString(ev.Query) {
			if err := sub.handler(ctx, ev); err != nil {
				return err
			}
			return nil
		}
	}
	return nil
}

// DispatchCallback routes a callback to its registered handler.
// Returns ErrCallbackNotFound when no handler is registered for the token
// embedded in ev.Data.
func (r *HandlerRegistry) DispatchCallback(ctx context.Context, bot *InlineBot, ev *CallbackEvent) error {
	token, _ := ParseInlineData(ev.Data)
	handler, ok := r.GetCallback(token, ev.UserID)
	if !ok {
		return nil // no handler registered; silently ignore
	}
	return handler(ctx, ev)
}

// Cleanup removes all expired callback entries and returns the number removed.
func (r *HandlerRegistry) Cleanup() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	removed := 0
	now := time.Now()
	for token, entry := range r.callbackHandlers {
		if !entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt) {
			delete(r.callbackHandlers, token)
			removed++
		}
	}
	return removed
}

// peerFromRaw converts a tg.PeerClass (from an update) into a
// tg.InputPeerClass suitable for bot API calls.
// For channels and users, the access hash is unknown at this layer and set
// to 0; callers that need the real hash must resolve the peer separately.
func peerFromRaw(peer tg.PeerClass) (tg.InputPeerClass, error) {
	if peer == nil {
		return nil, fmt.Errorf("nil peer")
	}
	switch p := peer.(type) {
	case *tg.PeerUser:
		return &tg.InputPeerUser{UserID: p.UserID}, nil
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, nil
	case *tg.PeerChannel:
		return &tg.InputPeerChannel{ChannelID: p.ChannelID}, nil
	}
	return nil, fmt.Errorf("unknown peer type %T", peer)
}
