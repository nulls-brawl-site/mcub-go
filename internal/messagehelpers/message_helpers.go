// Package messagehelpers provides helpers for sending Telegram messages with
// HTML markup via the gotd/td client.
// Ported from utils/message_helpers.py.
// SPDX-License-Identifier: MIT
package messagehelpers

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/gotd/td/tg"

	"github.com/nulls-brawl-site/mcub-go/internal/htmlparser"
)

// ---- Regex patterns ---------------------------------------------------------

var (
	customEmojiTagRE = regexp.MustCompile(`(?i)<tg-emoji[^>]*>(.*?)</tg-emoji>`)
	legacyEmojiAltRE = regexp.MustCompile(`(?i)<img[^>]*src="tg://emoji\?id=[^"]+"[^>]*alt="([^"]*)"[^>]*>`)
	legacyEmojiRE    = regexp.MustCompile(`(?i)<img[^>]*src="tg://emoji\?id=[^"]+"[^>]*>`)
	whitespaceRE     = regexp.MustCompile(`\s+`)

	supportedTagRE = []*regexp.Regexp{
		regexp.MustCompile(`(?i)</(?:b|strong|i|em|u|s|del|code|pre|blockquote|a|tg-spoiler|spoiler)>`),
		regexp.MustCompile(`(?i)<(?:b|strong|i|em|u|s|del|code|tg-spoiler|spoiler)(?:\s[^>]*)?>`),
		regexp.MustCompile(`(?i)<pre(?:\s[^>]*)?>`),
		regexp.MustCompile(`(?i)<blockquote(?:\s[^>]*)?>`),
		regexp.MustCompile(`(?i)<a\s[^>]*>`),
		regexp.MustCompile(`(?i)<br(?:\s[^>]*)?>`),
	}
)

// ---- CleanHTMLFallback -------------------------------------------------------

// CleanHTMLFallback removes all HTML formatting tags and returns plain text.
// Used as a fallback when full HTML parsing fails.
func CleanHTMLFallback(htmlText string) string {
	if htmlText == "" {
		return ""
	}
	text := customEmojiTagRE.ReplaceAllString(htmlText, "$1")
	text = legacyEmojiAltRE.ReplaceAllString(text, "$1")
	text = legacyEmojiRE.ReplaceAllString(text, "")
	for _, p := range supportedTagRE {
		text = p.ReplaceAllString(text, "")
	}
	text = html.UnescapeString(text)
	text = strings.TrimSpace(whitespaceRE.ReplaceAllString(text, " "))
	return text
}

// ---- UTF-16 helpers ----------------------------------------------------------

// utf16Len returns the number of UTF-16 code units for s.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// ---- TruncateWithEntities ---------------------------------------------------

// TruncateWithEntities truncates text and its associated entities to fit
// within maxLen UTF-16 code units (Telegram's message length limit).
func TruncateWithEntities(text string, entities []tg.MessageEntityClass, maxLen int) (string, []tg.MessageEntityClass) {
	if maxLen <= 0 {
		maxLen = 4096
	}
	if utf16Len(text) <= maxLen {
		return text, entities
	}

	// Truncate rune-by-rune until we hit the limit.
	var sb strings.Builder
	current := 0
	for _, r := range text {
		rLen := 1
		if r >= 0x10000 {
			rLen = 2
		}
		if current+rLen > maxLen {
			break
		}
		sb.WriteRune(r)
		current += rLen
	}
	truncated := sb.String()
	truncLen := utf16Len(truncated)

	// Adjust entities.
	var kept []tg.MessageEntityClass
	for _, e := range entities {
		type offsetter interface {
			GetOffset() int
			GetLength() int
		}
		if oe, ok := e.(offsetter); ok {
			off := oe.GetOffset()
			length := oe.GetLength()
			if off+length <= truncLen {
				kept = append(kept, e)
				// Entities that fit entirely are kept as-is.
			}
			// Entities that start inside but end outside are dropped for safety.
		}
	}
	return truncated, kept
}

// ---- ParseHTML wrapper -------------------------------------------------------

// ParseHTML parses html-formatted text and returns plain text plus entities.
// It delegates to the internal htmlparser package.
func ParseHTML(htmlText string) (string, []tg.MessageEntityClass, error) {
	return htmlparser.ParseHTML(htmlText)
}

// ---- GetRawText -------------------------------------------------------------

// GetRawText reconstructs the original HTML from a tg.Message using its
// text and formatting_entities fields.
func GetRawText(msg *tg.Message) string {
	if msg == nil {
		return ""
	}
	text := msg.Message
	entities := msg.Entities
	if len(entities) == 0 {
		return html.EscapeString(text)
	}
	return htmlparser.TelegramToHTML(text, entities)
}

// ---- SendRequest / Outgoing message builder ---------------------------------

// OutgoingMessage is a platform-agnostic representation of a message to be sent.
type OutgoingMessage struct {
	Text     string
	Entities []tg.MessageEntityClass
	// HTML source (before parsing). Set when constructed from HTML.
	HTML string
}

// BuildFromHTML parses htmlText and returns an OutgoingMessage ready to be
// sent via the Telegram API.
func BuildFromHTML(htmlText string, maxLen int) (*OutgoingMessage, error) {
	text, entities, err := ParseHTML(htmlText)
	if err != nil {
		// Fallback: strip tags.
		text = CleanHTMLFallback(htmlText)
		entities = nil
	}
	if maxLen > 0 {
		text, entities = TruncateWithEntities(text, entities, maxLen)
	}
	return &OutgoingMessage{Text: text, Entities: entities, HTML: htmlText}, nil
}

// ---- utf16.Decode helper (used by GetRawText) --------------------------------

var _ = utf16.Decode // ensure the import is used
