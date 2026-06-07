// Package htmlparser provides bidirectional conversion between HTML-formatted
// text and Telegram message entities (gotd/td tg.MessageEntityClass).
//
// Ported from utils/html_parser.py and utils/raw_html.py.
// SPDX-License-Identifier: MIT
package htmlparser

import (
	"html"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/gotd/td/tg"
)

// ---- UTF-16 helpers (mirrors Python _utf16_len / _utf16_slice) ---------------

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

// utf16Slice extracts the substring that starts at UTF-16 offset and spans
// length UTF-16 code units.
func utf16Slice(s string, offset, length int) string {
	if s == "" || length <= 0 {
		return ""
	}
	u := utf16.Encode([]rune(s))
	if offset >= len(u) {
		return ""
	}
	end := offset + length
	if end > len(u) {
		end = len(u)
	}
	return string(utf16.Decode(u[offset:end]))
}

// ---- EscapeHTML / UnescapeHTML ----------------------------------------------

// EscapeHTML escapes &, <, >, and " for safe embedding in HTML.
func EscapeHTML(text string) string {
	return html.EscapeString(text)
}

// UnescapeHTML converts HTML entities back to plain characters.
func UnescapeHTML(text string) string {
	return html.UnescapeString(text)
}

// ---- IsHTMLFormatted --------------------------------------------------------

// IsHTMLFormatted returns true when the text appears to contain Telegram HTML
// markup (bold, italic, code, links, etc.).
func IsHTMLFormatted(text string) bool {
	tags := []string{"<b>", "<i>", "<u>", "<s>", "<code", "<pre", "<a ", "<tg-", "<blockquote"}
	for _, t := range tags {
		if strings.Contains(text, t) {
			return true
		}
	}
	return false
}

// ---- StripHTML --------------------------------------------------------------

// StripHTML removes all HTML tags from text and unescapes entities.
func StripHTML(htmlText string) string {
	var buf strings.Builder
	inTag := false
	for _, r := range htmlText {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			buf.WriteRune(r)
		}
	}
	return html.UnescapeString(buf.String())
}

// ---- BlockquoteAttrs --------------------------------------------------------

// BlockquoteAttrs holds parsed attributes from a <blockquote> tag.
type BlockquoteAttrs struct {
	Expandable bool
	Collapsed  bool
}

// ParseBlockquoteAttrs parses the attribute string of a <blockquote> element.
// E.g. `expandable` or `expandable="true"` sets Expandable and Collapsed.
func ParseBlockquoteAttrs(attrs string) BlockquoteAttrs {
	ba := BlockquoteAttrs{}
	lower := strings.ToLower(attrs)
	if strings.Contains(lower, "expandable") {
		ba.Expandable = true
		ba.Collapsed = true
	}
	return ba
}

// ---- Simple HTML tokeniser --------------------------------------------------

type tokenKind int

const (
	tokText tokenKind = iota
	tokOpen
	tokClose
	tokSelfClose
)

type token struct {
	kind  tokenKind
	raw   string // full tag string incl. angle brackets
	tag   string // lower-case tag name
	attrs map[string]string
}

// tokenise splits HTML into a stream of tokens. It is intentionally simple and
// handles only the subset of HTML that Telegram's Bot API can produce.
func tokenise(src string) []token {
	var tokens []token
	i := 0
	n := len(src)
	for i < n {
		if src[i] != '<' {
			j := strings.IndexByte(src[i:], '<')
			if j < 0 {
				tokens = append(tokens, token{kind: tokText, raw: src[i:]})
				break
			}
			tokens = append(tokens, token{kind: tokText, raw: src[i : i+j]})
			i += j
			continue
		}
		j := strings.IndexByte(src[i:], '>')
		if j < 0 {
			tokens = append(tokens, token{kind: tokText, raw: src[i:]})
			break
		}
		raw := src[i : i+j+1]
		i += j + 1

		inner := raw[1 : len(raw)-1]
		selfClose := strings.HasSuffix(inner, "/")
		if selfClose {
			inner = strings.TrimSuffix(inner, "/")
		}
		inner = strings.TrimSpace(inner)
		closing := strings.HasPrefix(inner, "/")
		if closing {
			inner = strings.TrimPrefix(inner, "/")
			inner = strings.TrimSpace(inner)
			parts := strings.Fields(inner)
			tagName := ""
			if len(parts) > 0 {
				tagName = strings.ToLower(parts[0])
			}
			tokens = append(tokens, token{kind: tokClose, raw: raw, tag: tagName})
			continue
		}

		parts := strings.Fields(inner)
		tagName := ""
		if len(parts) > 0 {
			tagName = strings.ToLower(parts[0])
		}
		attrStr := ""
		if len(parts) > 1 {
			attrStr = inner[len(parts[0]):]
		}
		attrs := parseAttrs(attrStr)

		kind := tokOpen
		if selfClose || tagName == "br" {
			kind = tokSelfClose
		}
		tokens = append(tokens, token{kind: kind, raw: raw, tag: tagName, attrs: attrs})
	}
	return tokens
}

// parseAttrs parses key="value" or key attribute pairs.
func parseAttrs(s string) map[string]string {
	out := make(map[string]string)
	s = strings.TrimSpace(s)
	for len(s) > 0 {
		// skip whitespace
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			break
		}
		// find key
		eqIdx := strings.IndexAny(s, "= \t")
		if eqIdx < 0 {
			out[strings.ToLower(s)] = ""
			break
		}
		key := strings.ToLower(s[:eqIdx])
		s = s[eqIdx:]
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "=") {
			out[key] = ""
			continue
		}
		s = s[1:] // skip '='
		s = strings.TrimSpace(s)
		if len(s) == 0 {
			out[key] = ""
			break
		}
		var value string
		if s[0] == '"' || s[0] == '\'' {
			q := s[0]
			end := strings.IndexByte(s[1:], q)
			if end < 0 {
				value = s[1:]
				s = ""
			} else {
				value = s[1 : end+1]
				s = s[end+2:]
			}
		} else {
			end := strings.IndexAny(s, " \t")
			if end < 0 {
				value = s
				s = ""
			} else {
				value = s[:end]
				s = s[end:]
			}
		}
		out[key] = html.UnescapeString(value)
	}
	return out
}

// ---- ParseHTML --------------------------------------------------------------

// openEntity tracks an entity that has been opened but not yet closed.
type openEntity struct {
	tag      string
	entity   tg.MessageEntityClass
	offset   int // UTF-16 offset at open
}

// ParseHTML converts an HTML-formatted string into plain text plus a slice of
// Telegram message entities. Supports the full set of tags that Telegram
// clients can generate: <b>, <strong>, <i>, <em>, <u>, <s>, <del>, <strike>,
// <code>, <pre language="">, <a href="">, <tg-spoiler>, <spoiler>,
// <blockquote expandable="">, <tg-emoji emoji-id="">, <br>.
func ParseHTML(htmlText string) (string, []tg.MessageEntityClass, error) {
	var text strings.Builder
	var entities []tg.MessageEntityClass
	openStack := make([]openEntity, 0, 8)
	u16Pos := 0

	toks := tokenise(html.UnescapeString(htmlText))
	// Re-parse without pre-unescaping — tokeniser handles that
	toks = tokenise(htmlText)

	for _, tok := range toks {
		switch tok.kind {
		case tokText:
			decoded := html.UnescapeString(tok.raw)
			text.WriteString(decoded)
			u16Pos += utf16Len(decoded)

		case tokSelfClose:
			if tok.tag == "br" {
				text.WriteByte('\n')
				u16Pos++
			}

		case tokOpen:
			var ent tg.MessageEntityClass
			switch tok.tag {
			case "b", "strong":
				ent = &tg.MessageEntityBold{Offset: int(u16Pos)}
			case "i", "em":
				ent = &tg.MessageEntityItalic{Offset: int(u16Pos)}
			case "u":
				ent = &tg.MessageEntityUnderline{Offset: int(u16Pos)}
			case "s", "del", "strike":
				ent = &tg.MessageEntityStrike{Offset: int(u16Pos)}
			case "code":
				ent = &tg.MessageEntityCode{Offset: int(u16Pos)}
			case "pre":
				lang := tok.attrs["language"]
				ent = &tg.MessageEntityPre{Offset: int(u16Pos), Language: lang}
			case "tg-spoiler", "spoiler":
				ent = &tg.MessageEntitySpoiler{Offset: int(u16Pos)}
		case "blockquote":
			ent = &tg.MessageEntityBlockquote{Offset: int(u16Pos)}
			case "a":
				href := tok.attrs["href"]
				if strings.HasPrefix(href, "mailto:") {
					ent = &tg.MessageEntityEmail{Offset: int(u16Pos)}
				} else if href != "" {
					ent = &tg.MessageEntityTextURL{Offset: int(u16Pos), URL: href}
				}
			case "tg-emoji":
				rawID := tok.attrs["emoji-id"]
				if rawID == "" {
					rawID = tok.attrs["document_id"]
				}
				if id, err := strconv.ParseInt(rawID, 10, 64); err == nil {
					ent = &tg.MessageEntityCustomEmoji{Offset: int(u16Pos), DocumentID: id}
				}
			}
			if ent != nil {
				openStack = append(openStack, openEntity{tag: tok.tag, entity: ent, offset: u16Pos})
			}

		case tokClose:
			// Find the most-recently opened entity for this tag.
			for j := len(openStack) - 1; j >= 0; j-- {
				if openStack[j].tag == tok.tag {
					oe := openStack[j]
					length := u16Pos - oe.offset
					if length > 0 {
						setEntityLength(oe.entity, oe.offset, length)
						entities = append(entities, oe.entity)
					}
					// Remove from stack.
					openStack = append(openStack[:j], openStack[j+1:]...)
					break
				}
			}
		}
	}

	// Sort entities: by offset asc, length desc (outer first).
	sort.Slice(entities, func(i, j int) bool {
		oi, li := entityOffsetLen(entities[i])
		oj, lj := entityOffsetLen(entities[j])
		if oi != oj {
			return oi < oj
		}
		return li > lj
	})

	return text.String(), entities, nil
}

// setEntityLength patches the offset+length fields via a type switch because
// the tg.MessageEntityClass interface does not expose setters.
func setEntityLength(e tg.MessageEntityClass, offset, length int) {
	switch v := e.(type) {
	case *tg.MessageEntityBold:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityItalic:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityUnderline:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityStrike:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityCode:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityPre:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntitySpoiler:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityBlockquote:
		v.Offset = int(offset)
		v.Length = int(length)
	case *tg.MessageEntityTextURL:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityEmail:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityCustomEmoji:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityMention:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityMentionName:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityHashtag:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityURL:
		v.Offset, v.Length = int(offset), int(length)
	case *tg.MessageEntityBotCommand:
		v.Offset, v.Length = int(offset), int(length)
	}
}

// entityOffsetLen returns the offset and length for any entity.
func entityOffsetLen(e tg.MessageEntityClass) (offset, length int) {
	switch v := e.(type) {
	case *tg.MessageEntityBold:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityItalic:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityUnderline:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityStrike:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityCode:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityPre:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntitySpoiler:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityBlockquote:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityTextURL:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityEmail:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityCustomEmoji:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityMention:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityMentionName:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityHashtag:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityURL:
		return int(v.Offset), int(v.Length)
	case *tg.MessageEntityBotCommand:
		return int(v.Offset), int(v.Length)
	default:
		return 0, 0
	}
}

// ---- TelegramToHTML / ToHTML ------------------------------------------------

// entityEvent is used by the sweep-line renderer.
type entityEvent struct {
	pos      int
	isEnd    bool
	priority int // larger = higher (used for tie-breaking)
	idx      int
	entity   tg.MessageEntityClass
}

// TelegramToHTML converts plain text + Telegram entities back to HTML.
// Mirrors the Python HTMLDecorator.unparse() sweep-line algorithm.
func TelegramToHTML(text string, entities []tg.MessageEntityClass) string {
	return ToHTML(text, entities)
}

// ToHTML is an alias for TelegramToHTML.
func ToHTML(text string, entities []tg.MessageEntityClass) string {
	if len(entities) == 0 {
		return html.EscapeString(text)
	}

	u16 := utf16.Encode([]rune(text))
	totalLen := len(u16)

	// Build sweep-line events.
	evts := make([]entityEvent, 0, len(entities)*2)
	for i, ent := range entities {
		off, ln := entityOffsetLen(ent)
		evts = append(evts, entityEvent{pos: off, isEnd: false, priority: -ln, idx: i, entity: ent})
		evts = append(evts, entityEvent{pos: off + ln, isEnd: true, priority: ln, idx: i, entity: ent})
	}
	sort.Slice(evts, func(a, b int) bool {
		ea, eb := evts[a], evts[b]
		if ea.pos != eb.pos {
			return ea.pos < eb.pos
		}
		// ends before starts at same position
		if ea.isEnd != eb.isEnd {
			return ea.isEnd // end < start
		}
		return ea.priority < eb.priority
	})

	var buf strings.Builder
	var currentTags []tg.MessageEntityClass // tags currently open in HTML order
	var logicalStack []tg.MessageEntityClass
	lastPos := 0

	getChunk := func(start, length int) string {
		s := start * 2
		e := (start + length) * 2
		if s >= len(u16)*2 {
			return ""
		}
		if e > len(u16)*2 {
			e = len(u16) * 2
		}
		// work with u16 slice indices not byte indices
		si := start
		ei := start + length
		if si >= len(u16) {
			return ""
		}
		if ei > len(u16) {
			ei = len(u16)
		}
		return string(utf16.Decode(u16[si:ei]))
	}
	_ = getChunk

	writeChunk := func(start, end int) {
		if end <= start {
			return
		}
		si := start
		ei := end
		if si >= len(u16) {
			return
		}
		if ei > len(u16) {
			ei = len(u16)
		}
		buf.WriteString(html.EscapeString(string(utf16.Decode(u16[si:ei]))))
	}

	tagName := func(ent tg.MessageEntityClass) string {
		switch ent.(type) {
		case *tg.MessageEntityBold:
			return "b"
		case *tg.MessageEntityItalic:
			return "i"
		case *tg.MessageEntityUnderline:
			return "u"
		case *tg.MessageEntityStrike:
			return "s"
		case *tg.MessageEntityCode:
			return "code"
		case *tg.MessageEntityPre:
			return "pre"
		case *tg.MessageEntitySpoiler:
			return "tg-spoiler"
		case *tg.MessageEntityBlockquote:
			return "blockquote"
		case *tg.MessageEntityTextURL:
			return "a"
		case *tg.MessageEntityEmail:
			return "a"
		case *tg.MessageEntityCustomEmoji:
			return "tg-emoji"
		case *tg.MessageEntityMention:
			return "a"
		case *tg.MessageEntityMentionName:
			return "a"
		case *tg.MessageEntityHashtag:
			return "a"
		case *tg.MessageEntityURL:
			return "a"
		default:
			return "span"
		}
	}

	openTag := func(ent tg.MessageEntityClass) string {
		switch v := ent.(type) {
		case *tg.MessageEntityPre:
			if v.Language != "" {
				return "<pre language=\"" + html.EscapeString(v.Language) + "\">"
			}
			return "<pre>"
		case *tg.MessageEntityBlockquote:
			return "<blockquote>"
		case *tg.MessageEntityTextURL:
			return "<a href=\"" + html.EscapeString(v.URL) + "\">"
		case *tg.MessageEntityEmail:
			off, ln := entityOffsetLen(v)
			chunk := utf16Slice(text, off, ln)
			return "<a href=\"mailto:" + html.EscapeString(chunk) + "\">"
		case *tg.MessageEntityCustomEmoji:
			return "<tg-emoji emoji-id=\"" + strconv.FormatInt(v.DocumentID, 10) + "\">"
		case *tg.MessageEntityMentionName:
			return "<a href=\"tg://user?id=" + strconv.FormatInt(int64(v.UserID), 10) + "\">"
		case *tg.MessageEntityMention:
			off, ln := entityOffsetLen(v)
			chunk := utf16Slice(text, off, ln)
			domain := strings.TrimPrefix(chunk, "@")
			return "<a href=\"tg://resolve?domain=" + html.EscapeString(domain) + "\">"
		case *tg.MessageEntityURL:
			off, ln := entityOffsetLen(v)
			chunk := utf16Slice(text, off, ln)
			return "<a href=\"" + html.EscapeString(chunk) + "\">"
		default:
			return "<" + tagName(ent) + ">"
		}
	}

	closeTag := func(ent tg.MessageEntityClass) string {
		return "</" + tagName(ent) + ">"
	}

	for _, ev := range evts {
		// Flush text before this position.
		if ev.pos > lastPos {
			writeChunk(lastPos, ev.pos)
			lastPos = ev.pos
		}

		if ev.isEnd {
			// Remove from logical stack.
			for j := len(logicalStack) - 1; j >= 0; j-- {
				if logicalStack[j] == ev.entity {
					logicalStack = append(logicalStack[:j], logicalStack[j+1:]...)
					break
				}
			}
		} else {
			logicalStack = append(logicalStack, ev.entity)
			// Sort by length desc (longer = outer).
			sort.Slice(logicalStack, func(a, b int) bool {
				_, la := entityOffsetLen(logicalStack[a])
				_, lb := entityOffsetLen(logicalStack[b])
				return la > lb
			})
		}

		// Find common prefix length between currentTags and logicalStack.
		common := 0
		for common < len(currentTags) && common < len(logicalStack) {
			if currentTags[common] == logicalStack[common] {
				common++
			} else {
				break
			}
		}

		// Close mismatched tags (from innermost outward).
		for len(currentTags) > common {
			top := currentTags[len(currentTags)-1]
			buf.WriteString(closeTag(top))
			currentTags = currentTags[:len(currentTags)-1]
		}

		// Open new tags.
		for len(currentTags) < len(logicalStack) {
			ent := logicalStack[len(currentTags)]
			buf.WriteString(openTag(ent))
			currentTags = append(currentTags, ent)
		}
	}

	// Remaining text.
	if lastPos < totalLen {
		writeChunk(lastPos, totalLen)
	}

	// Close remaining tags.
	for len(currentTags) > 0 {
		top := currentTags[len(currentTags)-1]
		buf.WriteString(closeTag(top))
		currentTags = currentTags[:len(currentTags)-1]
	}

	return buf.String()
}


