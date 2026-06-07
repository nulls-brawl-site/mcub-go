package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

const (
	emojiTrLoading = `<tg-emoji emoji-id="5323463142775202324">🏓</tg-emoji>`
	emojiTrError   = `<tg-emoji emoji-id="5388785832956016892">❌</tg-emoji>`
	emojiTrGlobe   = "🌐"
)

// trModule implements the .tr translation command using the free Google Translate API.
type trModule struct {
	k          *kernel.Kernel
	httpClient *http.Client
	defaultLang string
}

func newTrModule() loader.Module {
	return &trModule{
		httpClient:  &http.Client{Timeout: 12 * time.Second},
		defaultLang: "en",
	}
}

func (m *trModule) Name() string { return "tr" }

func (m *trModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("tr: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern

	// Load default language from DB (key "tr:lang").
	if lang, _, err := kern.DB.Get("tr:lang"); err == nil && lang != "" {
		m.defaultLang = lang
	}

	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

func (m *trModule) OnUnload(k interface{}) error {
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

func (m *trModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "tr",
			Description: "translate text using Google Translate. Usage: .tr [lang] <text> or reply to translate",
			Handler:     m.cmdTr,
		},
		{
			Name:        "trlang",
			Description: "set default translation language. Usage: .trlang <lang>",
			Handler:     m.cmdTrlang,
		},
	}
}

// parseArgs strips prefix+command and returns remaining tokens.
func (m *trModule) parseArgs(ev *events.NewMessage) []string {
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

// argsRaw returns everything after the command word.
func (m *trModule) argsRaw(ev *events.NewMessage) string {
	body := ev.Text()
	if m.k != nil {
		body = strings.TrimPrefix(body, m.k.Prefix())
	}
	idx := strings.IndexByte(body, ' ')
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(body[idx+1:])
}

// translateText calls the free Google Translate endpoint and returns the translated text.
// fromLang can be "auto" to let Google detect the source language.
func (m *trModule) translateText(ctx context.Context, text, toLang, fromLang string) (string, string, error) {
	if fromLang == "" {
		fromLang = "auto"
	}
	apiURL := fmt.Sprintf(
		"https://translate.googleapis.com/translate_a/single?client=gtx&sl=%s&tl=%s&dt=t&q=%s",
		url.QueryEscape(fromLang),
		url.QueryEscape(toLang),
		url.QueryEscape(text),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read response: %w", err)
	}

	// The response is a nested JSON array:
	// [[["translated","original",...], ...], ..., "detected_lang", ...]
	var raw []interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", "", fmt.Errorf("parse JSON: %w", err)
	}

	// Extract translation segments from raw[0].
	sentences, ok := raw[0].([]interface{})
	if !ok || len(sentences) == 0 {
		return "", "", fmt.Errorf("unexpected response format")
	}
	var parts []string
	for _, seg := range sentences {
		pair, ok := seg.([]interface{})
		if !ok || len(pair) == 0 {
			continue
		}
		if s, ok := pair[0].(string); ok {
			parts = append(parts, s)
		}
	}
	translated := strings.Join(parts, "")

	// Extract detected source language (raw[2] when fromLang == "auto").
	detectedLang := fromLang
	if fromLang == "auto" && len(raw) > 2 {
		if dl, ok := raw[2].(string); ok && dl != "" {
			detectedLang = dl
		}
	}

	return translated, detectedLang, nil
}

// parseTrArgs parses the arguments for .tr command.
// Supported forms:
//   - .tr text             → auto→defaultLang
//   - .tr ru text          → auto→ru
//   - .tr en:ru text       → en→ru
//   - (reply)              → auto→defaultLang (text from reply)
//   - (reply) ru           → auto→ru
func (m *trModule) parseTrArgs(rawArgs string) (fromLang, toLang, text string) {
	fromLang = "auto"
	toLang = m.defaultLang

	if rawArgs == "" {
		return
	}

	fields := strings.SplitN(rawArgs, " ", 2)

	if strings.Contains(fields[0], ":") {
		// "en:ru" or "auto:ru" form.
		parts := strings.SplitN(fields[0], ":", 2)
		fromLang = parts[0]
		toLang = parts[1]
		if len(fields) > 1 {
			text = fields[1]
		}
		return
	}

	// Check if first word is a 2-letter language code.
	if len(fields[0]) == 2 && isAlpha(fields[0]) {
		toLang = fields[0]
		if len(fields) > 1 {
			text = fields[1]
		}
		return
	}

	// Otherwise the whole rawArgs is the text.
	text = rawArgs
	return
}

func isAlpha(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// cmdTr implements the .tr command.
func (m *trModule) cmdTr(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	rawArgs := m.argsRaw(ev)
	fromLang, toLang, textToTranslate := m.parseTrArgs(rawArgs)

	// If no text was found in args, try to get it from a replied-to message.
	if textToTranslate == "" {
		if ev.IsReply && ev.ReplyToMsgID != 0 && m.k.Client != nil {
			msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
			if err == nil && len(msgs) > 0 {
				textToTranslate = msgs[0].Message
			}
		}
	}

	if textToTranslate == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>No text to translate.</b>\n"+
				"<i>Usage: <code>.tr [lang] text</code> or reply to a message.</i>",
				emojiTrError))
	}

	// Show loading indicator.
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s <b>Translating...</b>", emojiTrLoading)); err != nil {
		return err
	}

	translated, detectedLang, err := m.translateText(ctx, textToTranslate, toLang, fromLang)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Translation failed:</b> %v", emojiTrError, err))
	}

	srcLabel := detectedLang
	if fromLang != "auto" {
		srcLabel = fromLang
	}

	result := fmt.Sprintf(
		"%s <b>Translated</b> (%s→%s):\n<blockquote>%s</blockquote>",
		emojiTrGlobe, srcLabel, toLang, translated,
	)
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, result)
}

// cmdTrlang sets the default translation language.
func (m *trModule) cmdTrlang(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🌐 Current default language: <code>%s</code>\n"+
				"<i>Usage: <code>.trlang &lt;lang&gt;</code></i>", m.defaultLang))
	}
	lang := strings.ToLower(args[0])
	m.defaultLang = lang
	_ = m.k.DB.Set("tr:lang", lang)
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("✅ Default translation language set to <code>%s</code>", lang))
}
