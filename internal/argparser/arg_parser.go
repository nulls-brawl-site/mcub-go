// Package argparser provides command-argument parsing for MCUB modules.
//
// Ported from utils/arg_parser.py.
// SPDX-License-Identifier: MIT
package argparser

import (
	"strconv"
	"strings"
	"unicode"
)

// ---- ParseArgs / GetArgsRaw -------------------------------------------------

// ParseArgs splits a command string into the command name and positional
// arguments.
//
//	".ping hello world"  =>  cmd="ping", args=["hello","world"]
func ParseArgs(text, prefix string) (cmd string, args []string) {
	if prefix == "" {
		prefix = "."
	}
	if !strings.HasPrefix(text, prefix) {
		return "", nil
	}
	body := strings.TrimPrefix(text, prefix)
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil
	}
	parts := strings.SplitN(body, " ", 2)
	cmd = parts[0]
	if len(parts) > 1 {
		args = SplitRespectingQuotes(strings.TrimSpace(parts[1]))
	}
	return
}

// GetArgsRaw returns everything after the first word (the command).
//
//	".echo hello world"  =>  "hello world"
func GetArgsRaw(text, prefix string) string {
	if prefix == "" {
		prefix = "."
	}
	if !strings.HasPrefix(text, prefix) {
		return ""
	}
	body := strings.TrimPrefix(text, prefix)
	body = strings.TrimSpace(body)
	idx := strings.IndexFunc(body, unicode.IsSpace)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(body[idx:])
}

// ---- Flags ------------------------------------------------------------------

// Flags holds the result of ParseFlags.
type Flags struct {
	// Bools contains flags that were present but had no value (e.g. --verbose).
	Bools map[string]bool
	// Strings contains flags that had an explicit value (e.g. --output=file).
	Strings map[string]string
	// Remaining holds positional tokens that were not consumed as flag values.
	Remaining []string
}

// ParseFlags scans args for --flag / -f style options.
// Long flags: --key=value or --key value; short flags: -k value or -abc
// (combined bools). Returns remaining positional tokens in Remaining.
func ParseFlags(args []string) *Flags {
	f := &Flags{
		Bools:   make(map[string]bool),
		Strings: make(map[string]string),
	}
	i := 0
	for i < len(args) {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			key := arg[2:]
			if eq := strings.IndexByte(key, '='); eq >= 0 {
				f.Strings[key[:eq]] = key[eq+1:]
			} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				f.Strings[key] = args[i+1]
				i++
			} else {
				f.Bools[key] = true
			}
		} else if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			chars := arg[1:]
			if len(chars) > 1 {
				// Combined short flags: -abc
				for _, c := range chars {
					f.Bools[string(c)] = true
				}
			} else {
				key := chars
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					f.Strings[key] = args[i+1]
					i++
				} else {
					f.Bools[key] = true
				}
			}
		} else {
			f.Remaining = append(f.Remaining, arg)
		}
		i++
	}
	return f
}

// ---- PipelineSegment / ParsePipeline ----------------------------------------

// PipelineSegment represents one command stage in a pipeline expression.
type PipelineSegment struct {
	// Command is the raw command text (stripped, prefix included if present).
	Command string
	// Operator is the operator that precedes this segment:
	// "" (first), "|", "&&", "||", "&"
	Operator string
}

// IsPipeline returns true when text contains at least one pipeline operator
// outside quotes.
func IsPipeline(text string) bool {
	return len(ParsePipeline(text)) > 1
}

// ParsePipeline splits text into PipelineSegments honouring quoting and
// backslash escapes. Mirrors the Python PipelineParser implementation.
func ParsePipeline(text string) []PipelineSegment {
	var segments []PipelineSegment
	var buf strings.Builder
	var pendingOp string
	inQuotes := false
	var quoteChar byte
	i := 0
	n := len(text)

	for i < n {
		ch := text[i]

		if ch == '\\' && !inQuotes {
			i++
			if i >= n {
				break
			}
			// Longest-match escape for operators
			rest := text[i:]
			escaped := false
			for _, core := range []string{" && ", " || ", " | ", " &&", " ||", " |", "&&", "||", "|", "& ", "&"} {
				if strings.HasPrefix(rest, core) {
					buf.WriteString(strings.TrimSpace(core))
					i += len(core)
					escaped = true
					break
				}
			}
			if !escaped {
				buf.WriteByte(text[i])
				i++
			}
			continue
		}

		if (ch == '"' || ch == '\'') && !inQuotes {
			inQuotes = true
			quoteChar = ch
			buf.WriteByte(ch)
			i++
			continue
		}
		if inQuotes && ch == quoteChar {
			inQuotes = false
			buf.WriteByte(ch)
			i++
			continue
		}
		if inQuotes {
			buf.WriteByte(ch)
			i++
			continue
		}

		// Detect operators (longest match first)
		op, matched := detectOp(text, i)
		if matched > 0 {
			seg := strings.TrimSpace(buf.String())
			if seg != "" {
				segments = append(segments, PipelineSegment{Command: seg, Operator: pendingOp})
			}
			pendingOp = op
			buf.Reset()
			i += matched
			continue
		}

		buf.WriteByte(ch)
		i++
	}

	if seg := strings.TrimSpace(buf.String()); seg != "" {
		segments = append(segments, PipelineSegment{Command: seg, Operator: pendingOp})
	}
	return segments
}

// detectOp attempts to match a pipeline operator at position i in text.
// Returns the operator key and number of bytes consumed, or ("", 0).
func detectOp(text string, i int) (op string, consumed int) {
	rest := text[i:]
	// Order matters: longer operators before their prefixes.
	ops := []struct {
		literal string
		key     string
	}{
		{" && ", "&&"},
		{" || ", "||"},
		{" | ", "|"},
		{"& ", "&"},
	}
	for _, o := range ops {
		if strings.HasPrefix(rest, o.literal) {
			return o.key, len(o.literal)
		}
	}
	return "", 0
}

// ---- Quote / Unquote --------------------------------------------------------

// Quote wraps s in double quotes if it contains spaces or is empty.
func Quote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n\"'") {
		return strconv.Quote(s)
	}
	return s
}

// Unquote removes surrounding single or double quotes from s.
func Unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		inner := s[1 : len(s)-1]
		if s[0] == '"' {
			if unq, err := strconv.Unquote(s); err == nil {
				return unq
			}
		}
		return inner
	}
	return s
}

// ---- SplitRespectingQuotes --------------------------------------------------

// SplitRespectingQuotes splits text on whitespace while honouring single- and
// double-quoted spans and backslash escapes. Mirrors Python shlex.split().
func SplitRespectingQuotes(text string) []string {
	var tokens []string
	var cur strings.Builder
	inQuotes := false
	var quoteChar rune

	runes := []rune(text)
	i := 0
	for i < len(runes) {
		ch := runes[i]

		if ch == '\\' && !inQuotes && i+1 < len(runes) {
			i++
			cur.WriteRune(runes[i])
			i++
			continue
		}

		if (ch == '"' || ch == '\'') && !inQuotes {
			inQuotes = true
			quoteChar = ch
			i++
			continue
		}
		if inQuotes && ch == quoteChar {
			inQuotes = false
			i++
			continue
		}
		if inQuotes {
			cur.WriteRune(ch)
			i++
			continue
		}

		if unicode.IsSpace(ch) {
			if cur.Len() > 0 {
				tokens = append(tokens, cur.String())
				cur.Reset()
			}
			i++
			continue
		}

		cur.WriteRune(ch)
		i++
	}
	if cur.Len() > 0 || inQuotes {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// ---- ArgumentParser (full Python port) --------------------------------------

// Value is a parsed argument value. It can be a string, int64, float64, bool,
// or []Value (comma-separated list).
type Value interface{}

// ArgumentParser mirrors the Python ArgumentParser class.
type ArgumentParser struct {
	FullText string
	Prefix   string
	Command  string
	Args     []Value
	Kwargs   map[string]Value
	Flags    map[string]bool
	RawArgs  string
}

// NewArgumentParser creates and initialises an ArgumentParser from text.
func NewArgumentParser(text, prefix string) (*ArgumentParser, error) {
	if prefix == "" {
		prefix = "."
	}
	ap := &ArgumentParser{
		FullText: strings.TrimSpace(text),
		Prefix:   prefix,
		Kwargs:   make(map[string]Value),
		Flags:    make(map[string]bool),
	}
	if err := ap.parse(); err != nil {
		return nil, err
	}
	return ap, nil
}

func (ap *ArgumentParser) parse() error {
	if ap.FullText == "" {
		return nil
	}
	if !strings.HasPrefix(ap.FullText, ap.Prefix) {
		return nil // silently ignore non-command text
	}
	body := strings.TrimPrefix(ap.FullText, ap.Prefix)
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	idx := strings.IndexFunc(body, unicode.IsSpace)
	if idx < 0 {
		ap.Command = body
		return nil
	}
	ap.Command = body[:idx]
	ap.RawArgs = strings.TrimSpace(body[idx:])
	ap.parseArguments(ap.RawArgs)
	return nil
}

func (ap *ArgumentParser) parseArguments(s string) {
	tokens := SplitRespectingQuotes(s)
	i := 0
	for i < len(tokens) {
		tok := tokens[i]
		if strings.HasPrefix(tok, "--") {
			key := tok[2:]
			if eq := strings.IndexByte(key, '='); eq >= 0 {
				ap.Kwargs[key[:eq]] = parseValue(key[eq+1:])
			} else if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
				ap.Kwargs[key] = parseValue(tokens[i+1])
				i++
			} else {
				ap.Flags[key] = true
				ap.Kwargs[key] = true
			}
		} else if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			chars := tok[1:]
			if len(chars) > 1 {
				for _, c := range chars {
					ap.Flags[string(c)] = true
					ap.Kwargs[string(c)] = true
				}
			} else {
				key := chars
				if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "-") {
					ap.Kwargs[key] = parseValue(tokens[i+1])
					i++
				} else {
					ap.Flags[key] = true
					ap.Kwargs[key] = true
				}
			}
		} else {
			ap.Args = append(ap.Args, parseValue(tok))
		}
		i++
	}
}

// parseValue converts a token to the most specific type.
func parseValue(s string) Value {
	if s == "" {
		return s
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	switch strings.ToLower(s) {
	case "true", "yes", "on":
		return true
	case "false", "no", "off":
		return false
	}
	if strings.Contains(s, ",") {
		parts := strings.Split(s, ",")
		var list []Value
		for _, p := range parts {
			list = append(list, parseValue(strings.TrimSpace(p)))
		}
		return list
	}
	return s
}

// Get returns the positional argument at index, or defaultVal if out of range.
func (ap *ArgumentParser) Get(index int, defaultVal Value) Value {
	if index < 0 || index >= len(ap.Args) {
		return defaultVal
	}
	return ap.Args[index]
}

// GetFlag returns true if the flag name is set.
func (ap *ArgumentParser) GetFlag(flag string) bool {
	return ap.Flags[flag]
}

// GetKwarg returns the named argument value, or defaultVal if absent.
func (ap *ArgumentParser) GetKwarg(key string, defaultVal Value) Value {
	if v, ok := ap.Kwargs[key]; ok {
		return v
	}
	return defaultVal
}

// JoinArgs returns positional arguments from start to end joined by spaces.
func (ap *ArgumentParser) JoinArgs(start, end int) string {
	if end < 0 || end > len(ap.Args) {
		end = len(ap.Args)
	}
	var parts []string
	for _, v := range ap.Args[start:end] {
		parts = append(parts, valueToString(v))
	}
	return strings.Join(parts, " ")
}

func valueToString(v Value) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}
