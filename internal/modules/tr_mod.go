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
)

// trModule implements the .tr translation command using the free Google Translate API.
// Faithful port of tr.py (TrModule).
type trModule struct {
	k           *kernel.Kernel
	httpClient  *http.Client
	defaultLang string
}

func newTrModule() loader.Module {
	return &trModule{
		httpClient:  &http.Client{Timeout: 12 * time.Second},
		defaultLang: "ru", // matches tr.py default: "ru"
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
	}
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

// _translateText calls the free Google Translate endpoint and returns the translated text.
// Mirrors tr.py _translate_text(). Returns translated string (may be an error message on failure).
func (m *trModule) _translateText(ctx context.Context, text, dest string) (string, error) {
	apiURL := fmt.Sprintf(
		"https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=%s&dt=t&q=%s",
		url.QueryEscape(dest),
		url.QueryEscape(text),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		networkErr := s(m.k, "tr", "network_error")
		return "", fmt.Errorf("%s %v", networkErr, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	var raw []interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("%s", s(m.k, "tr", "decode_error"))
	}

	// Extract translation segments from raw[0].
	if len(raw) == 0 || raw[0] == nil {
		return "", fmt.Errorf("%s", s(m.k, "tr", "translation_failed"))
	}

	sentences, ok := raw[0].([]interface{})
	if !ok || len(sentences) == 0 {
		return "", fmt.Errorf("%s", s(m.k, "tr", "translation_failed"))
	}

	var parts []string
	for _, seg := range sentences {
		pair, ok := seg.([]interface{})
		if !ok || len(pair) == 0 {
			continue
		}
		if str, ok := pair[0].(string); ok {
			parts = append(parts, str)
		}
	}
	translated := strings.Join(parts, "")
	if translated == "" {
		return "", fmt.Errorf("%s", s(m.k, "tr", "translation_failed"))
	}
	return translated, nil
}

// cmdTr implements the .tr command.
// Matches tr.py cmd_tr() logic exactly.
func (m *trModule) cmdTr(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	rawArgs := m.argsRaw(ev)
	args := strings.Fields(rawArgs)

	targetLang := m.defaultLang
	var textToTranslate string

	if ev.IsReply && ev.ReplyToMsgID != 0 {
		// Has a reply — fetch reply text
		var replyText string
		msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
		if err == nil && len(msgs) > 0 && msgs[0] != nil {
			replyText = msgs[0].Message
		}

		// quote_text simulation: if args[0] looks like a 2-char lang code, override lang
		if len(args) > 0 && len(args[0]) == 2 && isAlpha(args[0]) {
			targetLang = args[0]
			if len(args) > 1 {
				textToTranslate = strings.Join(args[1:], " ")
			} else {
				textToTranslate = replyText
			}
		} else if len(args) == 0 {
			textToTranslate = replyText
		} else {
			textToTranslate = rawArgs
		}

		if textToTranslate == "" {
			textToTranslate = replyText
		}
	} else {
		// No reply
		if len(args) == 0 {
			// No args, no reply
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				s(m.k, "tr", "no_args"))
		} else if len(args) == 1 {
			arg1 := args[0]
			if len(arg1) == 2 && isAlpha(arg1) {
				// 2-char lang code, but no reply to translate
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					fmt.Sprintf(`%s <b>%s</b>`, emojiTrError, s(m.k, "tr", "specify_text")))
			}
			textToTranslate = arg1
		} else {
			// len(args) >= 2
			arg1 := args[0]
			if len(arg1) == 2 && isAlpha(arg1) {
				targetLang = arg1
				textToTranslate = strings.Join(args[1:], " ")
			} else {
				textToTranslate = strings.Join(args, " ")
			}
		}
	}

	if textToTranslate == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf(`%s <b>%s</b>`, emojiTrError, s(m.k, "tr", "no_text")))
	}

	// Show loading indicator (HTML parse mode).
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf(`%s <b>%s</b>`, emojiTrLoading, s(m.k, "tr", "loading"))); err != nil {
		return err
	}

	translated, err := m._translateText(ctx, textToTranslate, targetLang)
	if err != nil {
		// Generic translation error with exception text
		errStr := err.Error()
		if len(errStr) > 200 {
			errStr = errStr[:200]
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf(`%s <b>%s</b>`+"\n<code>%s</code>",
				emojiTrError, s(m.k, "tr", "translation_error"), errStr))
	}

	// Output: just the translated text, plain (no HTML wrapper) — matches Python's
	// `await status_msg.edit(translated)` with no parse_mode.
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, translated)
}

func isAlpha(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}
