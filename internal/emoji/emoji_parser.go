// Package emoji provides parsing utilities for Telegram custom emoji tags.
// Ported from utils/emoji_parser.py.
// SPDX-License-Identifier: MIT
package emoji

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

// TagPattern matches the Telegram HTML custom-emoji format used by the Bot API:
//
//	<tg-emoji emoji-id="123456789">🔮</tg-emoji>
var TagPattern = regexp.MustCompile(`<tg-emoji emoji-id="(\d+)">(.*?)</tg-emoji>`)

// LegacyTagPattern matches the MCUB internal format:
//
//	<emoji document_id=123456789>🔮</emoji>
var LegacyTagPattern = regexp.MustCompile(`<emoji\s+document_id=(\d+)>(.*?)</emoji>`)

// allPatterns is the ordered list of patterns tried when scanning text.
var allPatterns = []*regexp.Regexp{TagPattern, LegacyTagPattern}

// IsEmojiTag returns true when text contains at least one recognised emoji tag.
func IsEmojiTag(text string) bool {
	for _, p := range allPatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

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

// ParseToEntities converts custom emoji tags in text to plain text plus
// MessageEntityCustomEmoji entities that Telegram can render.
//
// Both <tg-emoji emoji-id="…"> and <emoji document_id=…> formats are supported.
func ParseToEntities(text string) (string, []tg.MessageEntityClass, error) {
	// Build a unified list of matches from all supported patterns.
	type match struct {
		start, end int
		docID      int64
		inner      string
	}

	var matches []match
	for _, p := range allPatterns {
		for _, m := range p.FindAllStringSubmatchIndex(text, -1) {
			// m[0],m[1] = full match; m[2],m[3] = group1 (id); m[4],m[5] = group2 (inner)
			idStr := text[m[2]:m[3]]
			inner := text[m[4]:m[5]]
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				continue
			}
			matches = append(matches, match{start: m[0], end: m[1], docID: id, inner: inner})
		}
	}

	// Sort by start position and deduplicate overlapping spans.
	if len(matches) == 0 {
		return text, nil, nil
	}
	// Simple insertion sort (usually few matches).
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && matches[j].start < matches[j-1].start; j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}

	var sb strings.Builder
	var entities []tg.MessageEntityClass
	offset := 0

	for _, m := range matches {
		if m.start < offset {
			continue // skip overlapping
		}
		// Append plain text before this tag.
		sb.WriteString(text[offset:m.start])
		// Append the inner (fallback) text.
		utf16Off := utf16Len(sb.String())
		sb.WriteString(m.inner)
		utf16Len16 := utf16Len(m.inner)
		entities = append(entities, &tg.MessageEntityCustomEmoji{
			Offset:     utf16Off,
			Length:     utf16Len16,
			DocumentID: m.docID,
		})
		offset = m.end
	}
	sb.WriteString(text[offset:])
	return sb.String(), entities, nil
}

// RemoveEmojiTags strips all recognised emoji tags, leaving only the fallback text.
func RemoveEmojiTags(text string) string {
	for _, p := range allPatterns {
		text = p.ReplaceAllStringFunc(text, func(s string) string {
			m := p.FindStringSubmatch(s)
			if len(m) >= 3 {
				return m[2]
			}
			return ""
		})
	}
	return text
}

// ExtractEmojiIDs returns all custom-emoji document IDs found in text.
func ExtractEmojiIDs(text string) []int64 {
	seen := make(map[int64]struct{})
	var ids []int64
	for _, p := range allPatterns {
		for _, m := range p.FindAllStringSubmatch(text, -1) {
			id, err := strconv.ParseInt(m[1], 10, 64)
			if err != nil {
				continue
			}
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// ReplaceEmojiTags replaces all recognised emoji tags with replacement.
func ReplaceEmojiTags(text, replacement string) string {
	for _, p := range allPatterns {
		text = p.ReplaceAllString(text, replacement)
	}
	return text
}

// FormatEmoji creates a <tg-emoji emoji-id="…"> tag for a given document ID.
func FormatEmoji(emojiID int64, fallback string) string {
	return fmt.Sprintf(`<tg-emoji emoji-id="%d">%s</tg-emoji>`, emojiID, fallback)
}

// FormatEmojiLegacy creates the MCUB internal <emoji document_id=…> tag.
func FormatEmojiLegacy(emojiID int64, fallback string) string {
	return fmt.Sprintf(`<emoji document_id=%d>%s</emoji>`, emojiID, fallback)
}

// EntitiesToHTML converts a plain Telegram message back to its HTML
// representation, wrapping custom emoji entities with <tg-emoji> tags.
func EntitiesToHTML(text string, entities []tg.MessageEntityClass) string {
	if len(entities) == 0 {
		return text
	}
	u := utf16.Encode([]rune(text))
	// Process entities in reverse offset order so indices stay valid.
	type span struct {
		off, length int
		docID       int64
	}
	var spans []span
	for _, e := range entities {
		if ce, ok := e.(*tg.MessageEntityCustomEmoji); ok {
			spans = append(spans, span{ce.Offset, ce.Length, ce.DocumentID})
		}
	}
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].off > spans[j-1].off; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}

	for _, s := range spans {
		if s.off >= len(u) {
			continue
		}
		end := s.off + s.length
		if end > len(u) {
			end = len(u)
		}
		inner := string(utf16.Decode(u[s.off:end]))
		tag := fmt.Sprintf(`<tg-emoji emoji-id="%d">%s</tg-emoji>`, s.docID, inner)
		// Replace in the UTF-16 slice.
		newU := make([]uint16, 0, len(u))
		newU = append(newU, u[:s.off]...)
		newU = append(newU, utf16.Encode([]rune(tag))...)
		newU = append(newU, u[end:]...)
		u = newU
	}
	return string(utf16.Decode(u))
}
