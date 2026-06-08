package modules

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/langpacks"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	mcubtypes "github.com/nulls-brawl-site/telegram-mcub-go/types"
)

// peerIDToInputPeer converts a numeric peer ID to a tg.InputPeerClass.
// Positive IDs are users; IDs < -999999999 are channels/supergroups;
// other negative IDs are legacy groups.
func peerIDToInputPeer(peerID int64) tg.InputPeerClass {
	if peerID > 0 {
		return &tg.InputPeerUser{UserID: peerID}
	}
	if peerID < -999999999 {
		chanID := -(peerID + 1000000000000)
		return &tg.InputPeerChannel{ChannelID: chanID}
	}
	return &tg.InputPeerChat{ChatID: -peerID}
}

// editHTML edits a message using HTML text parsed to Telegram entities.
func editHTML(ctx context.Context, k *kernel.Kernel, peerID int64, msgID int, html string) error {
	if k == nil || k.Client == nil {
		return nil
	}
	plain, entities, _ := mcubclient.ParseText(html, "html")
	if plain == "" {
		plain = html // fallback: send raw HTML as plain text
	}
	peer := peerIDToInputPeer(peerID)
	req := &tg.MessagesEditMessageRequest{
		Peer:     peer,
		ID:       msgID,
		Message:  plain,
		Entities: entities,
	}
	_, err := k.Client.API().MessagesEditMessage(ctx, req)
	return err
}

// editHTMLWithBanner edits a message with an attached web-page banner (URL preview).
// If bannerURL is empty, falls back to plain editHTML.
// invert=true places the preview above the text (matches Python invert_media=True).
func editHTMLWithBanner(ctx context.Context, k *kernel.Kernel, peerID int64, msgID int, html string, bannerURL string, invert bool) error {
	if k == nil || k.Client == nil {
		return nil
	}
	if bannerURL == "" {
		return editHTML(ctx, k, peerID, msgID, html)
	}
	plain, entities, _ := mcubclient.ParseText(html, "html")
	if plain == "" {
		plain = html
	}
	peer := peerIDToInputPeer(peerID)
	req := &tg.MessagesEditMessageRequest{
		Peer:        peer,
		ID:          msgID,
		Message:     plain,
		Entities:    entities,
		InvertMedia: invert,
		Media: &tg.InputMediaWebPage{
			URL:      bannerURL,
			Optional: true,
		},
	}
	_, err := k.Client.API().MessagesEditMessage(ctx, req)
	return err
}

// sendHTML sends a new HTML-formatted message to the given peer.
func sendHTML(ctx context.Context, k *kernel.Kernel, peerID int64, html string) error {
	if k == nil || k.Client == nil {
		return nil
	}
	plain, entities, _ := mcubclient.ParseText(html, "html")
	if plain == "" {
		plain = html
	}
	peer := peerIDToInputPeer(peerID)
	req := &tg.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  plain,
		RandomID: rand.Int63(),
		Entities: entities,
	}
	_, err := k.Client.API().MessagesSendMessage(ctx, req)
	return err
}

// sendHTMLWithButtons sends an HTML message with an inline keyboard.
func sendHTMLWithButtons(ctx context.Context, k *kernel.Kernel, peerID int64, html string, buttons mcubtypes.ButtonGrid) error {
	if k == nil || k.Client == nil {
		return nil
	}
	plain, entities, _ := mcubclient.ParseText(html, "html")
	if plain == "" {
		plain = html
	}
	peer := peerIDToInputPeer(peerID)
	req := &tg.MessagesSendMessageRequest{
		Peer:        peer,
		Message:     plain,
		RandomID:    rand.Int63(),
		Entities:    entities,
		ReplyMarkup: buttons.ToTLMarkup(),
	}
	_, err := k.Client.API().MessagesSendMessage(ctx, req)
	return err
}

// editHTMLWithButtons edits a message with HTML text and inline keyboard.
func editHTMLWithButtons(ctx context.Context, k *kernel.Kernel, peerID int64, msgID int, html string, buttons mcubtypes.ButtonGrid) error {
	if k == nil || k.Client == nil {
		return nil
	}
	plain, entities, _ := mcubclient.ParseText(html, "html")
	if plain == "" {
		plain = html
	}
	peer := peerIDToInputPeer(peerID)
	req := &tg.MessagesEditMessageRequest{
		Peer:        peer,
		ID:          msgID,
		Message:     plain,
		Entities:    entities,
		ReplyMarkup: buttons.ToTLMarkup(),
	}
	_, err := k.Client.API().MessagesEditMessage(ctx, req)
	return err
}

// sendDocument uploads and sends a local file as a document to a peer with an HTML caption.
func sendDocument(ctx context.Context, k *kernel.Kernel, peerID int64, filePath, htmlCaption string) error {
	if k == nil || k.Client == nil {
		return nil
	}
	plain, _, _ := mcubclient.ParseText(htmlCaption, "html")
	if plain == "" {
		plain = htmlCaption
	}
	_, err := k.Client.SendDocument(ctx, peerID, filePath, plain)
	return err
}

// newButton creates a callback inline button.
func newButton(text string, data string) *mcubtypes.Button {
	return &mcubtypes.Button{
		Text: text,
		Data: []byte(data),
	}
}

// newURLButton creates a URL inline button.
func newURLButton(text, url string) *mcubtypes.Button {
	return &mcubtypes.Button{
		Text: text,
		URL:  url,
	}
}

// s gets a plain string from the langpacks for the kernel's current language.
func s(k *kernel.Kernel, module, key string) string {
	if k == nil {
		return key
	}
	return langpacks.Default.Get(k.GetLanguage(), module, key)
}

// sf gets a langpack string with {placeholder} substitution from data.
func sf(k *kernel.Kernel, module, key string, data map[string]interface{}) string {
	if k == nil {
		return key
	}
	val := langpacks.Default.Get(k.GetLanguage(), module, key)
	for placeholder, v := range data {
		val = strings.ReplaceAll(val, "{"+placeholder+"}", fmt.Sprintf("%v", v))
	}
	return val
}
