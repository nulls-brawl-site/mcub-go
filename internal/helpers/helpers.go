// Package helpers provides utility functions for MCUB modules.
// Ported from utils/helpers.py.
// SPDX-License-Identifier: MIT
package helpers

import (
	"fmt"
	"html"
	"strings"
	"time"
	"unicode"
)

// ---- Argument parsing -------------------------------------------------------

// GetArgs splits the text of a command message into arguments, respecting
// quoted strings. It returns an empty slice when there are no arguments.
//
//	".cmd foo bar" → ["foo", "bar"]
//	`.cmd "hello world"` → ["hello world"]
func GetArgs(text string) []string {
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 {
		return nil
	}
	raw := strings.TrimSpace(parts[1])
	if raw == "" {
		return nil
	}
	return shellSplit(raw)
}

// GetArgsRaw returns everything after the first word (the command) as a raw string.
func GetArgsRaw(text string) string {
	idx := strings.IndexFunc(text, unicode.IsSpace)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(text[idx:])
}

// shellSplit is a minimal shell-like tokeniser that handles double-quoted strings.
func shellSplit(s string) []string {
	var tokens []string
	var cur strings.Builder
	inQuote := false
	quoteChar := rune(0)
	for _, r := range s {
		switch {
		case inQuote:
			if r == quoteChar {
				inQuote = false
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			inQuote = true
			quoteChar = r
		case unicode.IsSpace(r):
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// ---- HTML helpers -----------------------------------------------------------

// EscapeHTML escapes &, <, > and " for safe embedding in HTML.
func EscapeHTML(text string) string {
	return html.EscapeString(text)
}

// EscapeQuotes escapes HTML special chars and additionally escapes " to &quot;.
func EscapeQuotes(text string) string {
	return strings.ReplaceAll(EscapeHTML(text), `"`, "&quot;")
}

// LooksLikeHTML is a heuristic that returns true when text appears to contain
// Telegram-supported HTML tags.
func LooksLikeHTML(text string) bool {
	if !strings.Contains(text, "<") || !strings.Contains(text, ">") {
		return false
	}
	for _, tag := range []string{"<b>", "<i>", "<a ", "<code", "<pre"} {
		if strings.Contains(text, tag) {
			return true
		}
	}
	return false
}

// ---- Time formatting --------------------------------------------------------

// FormatTime converts a duration in seconds to a human-readable string.
//
//	FormatTime(90, false) → "1m 30s"
//	FormatTime(90, true)  → "1m 30s"
func FormatTime(seconds float64, detailed bool) string {
	s := int(seconds)
	if detailed {
		weeks := s / 604800
		s %= 604800
		days := s / 86400
		s %= 86400
		hours := s / 3600
		s %= 3600
		minutes := s / 60
		s %= 60

		var parts []string
		if weeks > 0 {
			parts = append(parts, fmt.Sprintf("%dw", weeks))
		}
		if days > 0 {
			parts = append(parts, fmt.Sprintf("%dd", days))
		}
		if hours > 0 {
			parts = append(parts, fmt.Sprintf("%dh", hours))
		}
		if minutes > 0 {
			parts = append(parts, fmt.Sprintf("%dm", minutes))
		}
		if s > 0 || len(parts) == 0 {
			parts = append(parts, fmt.Sprintf("%ds", s))
		}
		return strings.Join(parts, " ")
	}

	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	if s < 3600 {
		m := s / 60
		sec := s % 60
		if sec == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm %ds", m, sec)
	}
	h := s / 3600
	m := (s % 3600) / 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}

// FormatDate formats a Unix timestamp or time.Time to a string using fmt.
// Default format is "2006-01-02 15:04".
func FormatDate(t time.Time, format string) string {
	if format == "" {
		format = "2006-01-02 15:04"
	}
	return t.Format(format)
}

// FormatDateUnix is like FormatDate but accepts a Unix timestamp.
func FormatDateUnix(unix int64, format string) string {
	return FormatDate(time.Unix(unix, 0), format)
}

// FormatRelativeTime formats a Unix timestamp as a relative string like
// "5 minutes ago".
func FormatRelativeTime(unix int64) string {
	diff := time.Since(time.Unix(unix, 0))
	sec := int(diff.Seconds())
	switch {
	case sec < 60:
		return "just now"
	case sec < 3600:
		m := sec / 60
		return pluralAgo(m, "minute")
	case sec < 86400:
		h := sec / 3600
		return pluralAgo(h, "hour")
	case sec < 604800:
		d := sec / 86400
		return pluralAgo(d, "day")
	case sec < 2592000:
		w := sec / 604800
		return pluralAgo(w, "week")
	default:
		return FormatDateUnix(unix, "")
	}
}

func pluralAgo(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s ago", unit)
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}

// ---- Prefix / prefix detection ---------------------------------------------

// GetPrefix returns the command prefix from any object that exposes it.
// It tries the common attribute names used throughout the kernel.
func GetPrefix(target interface{}) string {
	type prefixer interface{ GetPrefix() string }
	if p, ok := target.(prefixer); ok {
		return p.GetPrefix()
	}
	return "."
}
