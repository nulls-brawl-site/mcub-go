// SPDX-License-Identifier: MIT
// Payments / Telegram Stars system module for MCUB-Go userbot.
// Provides .stars, .starshistory, .stargifts commands.

package modules

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji IDs for the payments module (exact IDs).
const (
	emojiPayStar    = `<tg-emoji emoji-id="5438496463044752972">⭐️</tg-emoji>`
	emojiPayIn      = `<tg-emoji emoji-id="5244837092042750681">📈</tg-emoji>`
	emojiPayOut     = `<tg-emoji emoji-id="5246762912428603768">📉</tg-emoji>`
	emojiPayGift    = `<tg-emoji emoji-id="5461151367559141950">🎉</tg-emoji>`
	emojiPayError   = `<tg-emoji emoji-id="5388785832956016892">❌</tg-emoji>`
	emojiPayLoading = `<tg-emoji emoji-id="5341715473882955310">⚙️</tg-emoji>`
)

// paymentsModule implements the "payments" system module.
type paymentsModule struct {
	k *kernel.Kernel
}

func newPaymentsModule() loader.Module { return &paymentsModule{} }

// Name implements loader.Module.
func (m *paymentsModule) Name() string { return "payments" }

// OnLoad implements loader.Module.
func (m *paymentsModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("payments: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *paymentsModule) OnUnload(k interface{}) error {
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
func (m *paymentsModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "stars", Description: "show current Telegram Stars balance", Handler: m.cmdStars},
		{Name: "starshistory", Description: "[limit] show recent stars transactions (default 10, max 50)", Handler: m.cmdStarsHistory},
		{Name: "stargifts", Description: "[limit] show saved star gifts (default 10)", Handler: m.cmdStarGifts},
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// .stars — show balance
// ──────────────────────────────────────────────────────────────────────────────

func (m *paymentsModule) cmdStars(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		emojiPayLoading+" <i>Fetching balance...</i>"); err != nil {
		return err
	}

	balance, err := m.k.Client.GetStarBalance(ctx)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Error:</b> <code>%s</code>", emojiPayError, payHTMLEsc(err.Error())))
	}

	text := fmt.Sprintf(
		"%s <b>Stars Balance</b>\n"+
			"💎 <b>Balance:</b> <code>%d ★</code>",
		emojiPayStar, balance,
	)
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, text)
}

// ──────────────────────────────────────────────────────────────────────────────
// .starshistory [limit]
// ──────────────────────────────────────────────────────────────────────────────

func (m *paymentsModule) cmdStarsHistory(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	limit := 10
	if raw := strings.TrimSpace(getArgsRaw(ev, m.k)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 {
			limit = n
		} else {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				emojiPayError+" <b>Invalid limit.</b> Use a number 1–50.")
		}
	}
	if limit > 50 {
		limit = 50
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		emojiPayLoading+" <i>Fetching transactions...</i>"); err != nil {
		return err
	}

	txs, err := m.k.Client.GetStarTransactions(ctx, limit, false, false)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Error:</b> <code>%s</code>", emojiPayError, payHTMLEsc(err.Error())))
	}

	if len(txs) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Stars History</b>\n\n<i>No transactions found.</i>", emojiPayStar))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>Stars History</b> (last %d)\n\n", emojiPayStar, len(txs)))

	for _, tx := range txs {
		sb.WriteString(formatTx(tx))
		sb.WriteByte('\n')
	}
	sb.WriteString("\n<i>Use .starshistory [limit] for more</i>")

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// formatTx formats a single StarsTransaction as an HTML line.
func formatTx(tx *tg.StarsTransaction) string {
	if tx == nil {
		return "<i>empty</i>"
	}

	// Amount
	amount := int64(0)
	if tx.Amount != nil {
		switch v := tx.Amount.(type) {
		case *tg.StarsAmount:
			amount = v.Amount
		case *tg.StarsTonAmount:
			amount = v.Amount
		}
	}

	// Direction: outbound transactions have a negative or refund marker
	emoji := emojiPayIn
	sign := "+"
	if tx.Refund || tx.Failed {
		emoji = emojiPayOut
		sign = "-"
	}

	// Peer label
	peer := formatTxPeer(tx.Peer)

	// Date
	date := time.Unix(int64(tx.Date), 0).Format("2006-01-02 15:04")

	// ID (truncate to 12 runes)
	id := tx.ID
	if r := []rune(id); len(r) > 12 {
		id = string(r[:12]) + "…"
	}

	return fmt.Sprintf("%s <code>%s%d ★</code> — %s — %s — <code>%s</code>",
		emoji, sign, amount, payHTMLEsc(date), payHTMLEsc(peer), payHTMLEsc(id))
}

// formatTxPeer returns a human-readable label for a transaction peer.
func formatTxPeer(p tg.StarsTransactionPeerClass) string {
	if p == nil {
		return "unknown"
	}
	switch v := p.(type) {
	case *tg.StarsTransactionPeer:
		return fmt.Sprintf("peer(%d)", v.Peer)
	case *tg.StarsTransactionPeerAppStore:
		return "App Store"
	case *tg.StarsTransactionPeerPlayMarket:
		return "Play Market"
	case *tg.StarsTransactionPeerFragment:
		return "Fragment"
	case *tg.StarsTransactionPeerPremiumBot:
		return "Telegram"
	case *tg.StarsTransactionPeerUnsupported:
		return "unsupported"
	case *tg.StarsTransactionPeerAds:
		return "Telegram Ads"
	case *tg.StarsTransactionPeerAPI:
		return "API"
	default:
		return fmt.Sprintf("%T", p)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// .stargifts [limit]
// ──────────────────────────────────────────────────────────────────────────────

func (m *paymentsModule) cmdStarGifts(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	limit := 10
	if raw := strings.TrimSpace(getArgsRaw(ev, m.k)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 1 {
			limit = n
		}
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		emojiPayLoading+" <i>Fetching saved gifts...</i>"); err != nil {
		return err
	}

	gifts, err := m.k.Client.GetSavedGifts(ctx, limit)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Error:</b> <code>%s</code>", emojiPayError, payHTMLEsc(err.Error())))
	}

	if len(gifts) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Saved Star Gifts</b>\n\n<i>No saved gifts found.</i>", emojiPayGift))
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s <b>Saved Star Gifts</b> (%d)\n\n", emojiPayGift, len(gifts)))

	for i, g := range gifts {
		sb.WriteString(formatGift(i+1, g))
		sb.WriteByte('\n')
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, sb.String())
}

// formatGift formats a SavedStarGift as an HTML line.
func formatGift(idx int, g *tg.SavedStarGift) string {
	if g == nil {
		return fmt.Sprintf("• <b>Gift #%d</b>: <i>empty</i>", idx)
	}

	// Extract gift info from the StarGift field
	stars := int64(0)
	giftType := "unknown"
	if g.Gift != nil {
		switch v := g.Gift.(type) {
		case *tg.StarGift:
			stars = v.Stars
			giftType = "star_gift"
			_ = v
		case *tg.StarGiftUnique:
			giftType = fmt.Sprintf("unique(%s)", payHTMLEsc(v.Title))
		}
	}

	fromPeer := "unknown"
	if g.FromID != nil {
		fromPeer = fmt.Sprintf("%T", g.FromID)
	}

	if stars > 0 {
		return fmt.Sprintf("• <b>Gift #%d</b> [%s]: <code>%d ★</code> from %s",
			idx, giftType, stars, fromPeer)
	}
	return fmt.Sprintf("• <b>Gift #%d</b> [%s] from %s", idx, giftType, fromPeer)
}

// payHTMLEsc escapes special HTML characters for safe display.
// Named distinctly to avoid collision with other helpers in the package.
func payHTMLEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}
