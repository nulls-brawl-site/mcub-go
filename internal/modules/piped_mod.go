package modules

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"go/token"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// pipedModule provides piped/pipeline utility commands.
type pipedModule struct {
	k *kernel.Kernel
}

func newPipedModule() loader.Module { return &pipedModule{} }

// Name implements loader.Module.
func (m *pipedModule) Name() string { return "piped" }

// OnLoad implements loader.Module.
func (m *pipedModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("piped: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *pipedModule) OnUnload(k interface{}) error {
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
func (m *pipedModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "echo", Description: "[text] — echo text (supports pipe_input)", Handler: m.cmdEcho},
		{Name: "grep", Description: "[-v] [-r] <pattern> [text] — filter lines", Handler: m.cmdGrep},
		{Name: "head", Description: "[-N] [text] — first N lines (default 10)", Handler: m.cmdHead},
		{Name: "tail", Description: "[-N] [text] — last N lines (default 10)", Handler: m.cmdTail},
		{Name: "sort", Description: "[-r] [-u] [text] — sort lines", Handler: m.cmdSort},
		{Name: "uniq", Description: "[text] — remove duplicate lines", Handler: m.cmdUniq},
		{Name: "wc", Description: "[-l|-w|-c] [text] — count lines/words/chars", Handler: m.cmdWC},
		{Name: "calc", Description: "<expr> — calculate math expression", Handler: m.cmdCalc},
		{Name: "sed", Description: "s/from/to/[g] [text] — simple substitution", Handler: m.cmdSed},
		{Name: "strip", Description: "[-e] [text] — strip whitespace from each line", Handler: m.cmdStrip},
		{Name: "b64", Description: "[-d] [text] — base64 encode/decode", Handler: m.cmdB64},
		{Name: "jq", Description: "[key...] — format JSON / extract fields", Handler: m.cmdJSON},
		{Name: "sleep", Description: "<seconds> — sleep N seconds", Handler: m.cmdSleep},
		{Name: "delete", Description: "— delete the event message", Handler: m.cmdDelete},
	}
}

// ---------- helpers ----------

// pipeState extracts the pipeline capture state and pipe input for a command.
// isPiped = true when this command is running inside a pipeline.
func pipeState(ctx context.Context) (state *pybridge.PipelineCaptureState, pipeInput string, isPiped bool) {
	state = pybridge.PipelineCaptureFromContext(ctx)
	if state != nil {
		pipeInput = state.Input
		isPiped = state.IsPiped
	}
	return
}

// pipeEdit edits the message and, when in a pipe, records the output.
func (m *pipedModule) pipeEdit(ctx context.Context, ev *events.NewMessage, state *pybridge.PipelineCaptureState, text string) error {
	if state != nil {
		state.Capture(text)
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, text)
}

// args returns the raw text after the command word.
func (m *pipedModule) argsRaw(ev *events.NewMessage) string {
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

// ---------- .echo ----------

func (m *pipedModule) cmdEcho(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	text := args
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, fmt.Sprintf("%secho &lt;text&gt;", m.k.Prefix()))
	}
	return m.pipeEdit(ctx, ev, state, text)
}

// ---------- .grep ----------

func (m *pipedModule) cmdGrep(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	invert := false
	useRegex := false

	// Parse flags.
	for strings.HasPrefix(args, "-") {
		space := strings.IndexByte(args, ' ')
		var flag string
		if space < 0 {
			flag, args = args, ""
		} else {
			flag, args = args[:space], strings.TrimSpace(args[space+1:])
		}
		if strings.Contains(flag, "v") {
			invert = true
		}
		if strings.Contains(flag, "r") {
			useRegex = true
		}
	}

	// pattern and optional inline text.
	pattern, inlineText := "", ""
	if args != "" {
		parts := strings.SplitN(args, " ", 2)
		pattern = parts[0]
		if len(parts) > 1 {
			inlineText = parts[1]
		}
	}

	text := inlineText
	if text == "" {
		text = pipeInput
	}
	if pattern == "" || text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>grep [-v] [-r] &lt;pattern&gt; [text]</code>")
	}

	lines := strings.Split(text, "\n")
	var matched []string
	for _, line := range lines {
		var matches bool
		if useRegex {
			re, err := regexp.Compile(pattern)
			if err != nil {
				matches = strings.Contains(line, pattern)
			} else {
				matches = re.MatchString(line)
			}
		} else {
			matches = strings.Contains(line, pattern)
		}
		if matches != invert {
			matched = append(matched, line)
		}
	}

	if len(matched) == 0 {
		return m.pipeEdit(ctx, ev, state, "<i>No match found.</i>")
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(matched, "\n"))
}

// ---------- .head ----------

func (m *pipedModule) cmdHead(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	n := 10
	text := ""

	if strings.HasPrefix(args, "-") {
		parts := strings.SplitN(args, " ", 2)
		if num, err := strconv.Atoi(parts[0][1:]); err == nil {
			n = num
		}
		if len(parts) > 1 {
			text = parts[1]
		}
	} else {
		text = args
	}
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>head [-N] [text]</code>")
	}

	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(lines, "\n"))
}

// ---------- .tail ----------

func (m *pipedModule) cmdTail(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	n := 10
	text := ""

	if strings.HasPrefix(args, "-") {
		parts := strings.SplitN(args, " ", 2)
		if num, err := strconv.Atoi(parts[0][1:]); err == nil {
			n = num
		}
		if len(parts) > 1 {
			text = parts[1]
		}
	} else {
		text = args
	}
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>tail [-N] [text]</code>")
	}

	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(lines, "\n"))
}

// ---------- .sort ----------

func (m *pipedModule) cmdSort(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	reverse := false
	unique := false

	for strings.HasPrefix(args, "-") {
		space := strings.IndexByte(args, ' ')
		var flag string
		if space < 0 {
			flag, args = args, ""
		} else {
			flag, args = args[:space], strings.TrimSpace(args[space+1:])
		}
		if strings.Contains(flag, "r") {
			reverse = true
		}
		if strings.Contains(flag, "u") {
			unique = true
		}
	}

	text := args
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>sort [-r] [-u] [text]</code>")
	}

	lines := strings.Split(text, "\n")

	if unique {
		seen := map[string]bool{}
		deduped := lines[:0]
		for _, l := range lines {
			if !seen[l] {
				seen[l] = true
				deduped = append(deduped, l)
			}
		}
		lines = deduped
	}

	sort.Strings(lines)
	if reverse {
		for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
			lines[i], lines[j] = lines[j], lines[i]
		}
	}

	return m.pipeEdit(ctx, ev, state, strings.Join(lines, "\n"))
}

// ---------- .uniq ----------

func (m *pipedModule) cmdUniq(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	text := args
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>uniq [text]</code>")
	}

	lines := strings.Split(text, "\n")
	seen := map[string]bool{}
	var out []string
	for _, l := range lines {
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(out, "\n"))
}

// ---------- .wc ----------

func (m *pipedModule) cmdWC(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	mode := "l"
	text := ""

	if strings.HasPrefix(args, "-") {
		parts := strings.SplitN(args, " ", 2)
		flag := parts[0]
		if len(flag) > 1 {
			switch flag[1] {
			case 'l':
				mode = "l"
			case 'w':
				mode = "w"
			case 'c':
				mode = "c"
			}
		}
		if len(parts) > 1 {
			text = parts[1]
		}
	} else {
		text = args
	}
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>wc [-l|-w|-c] [text]</code>")
	}

	var result string
	switch mode {
	case "w":
		result = strconv.Itoa(len(strings.Fields(text)))
	case "c":
		result = strconv.Itoa(len(text))
	default:
		result = strconv.Itoa(len(strings.Split(text, "\n")))
	}

	return m.pipeEdit(ctx, ev, state, result)
}

// ---------- .calc ----------

// safeCalc evaluates a simple arithmetic expression (no exec).
func safeCalc(expr string) (string, error) {
	expr = strings.TrimSpace(expr)
	// Use big.Float for precision; support basic operators via go/token scanning.
	// Simple recursive descent parser for +, -, *, /, (, ).
	val, _, err := parseExpr(expr, 0)
	if err != nil {
		return "", err
	}
	// Format nicely.
	if val.IsInt() {
		i, _ := val.Int(nil)
		return i.String(), nil
	}
	return val.Text('f', 10), nil
}

func parseExpr(s string, pos int) (*big.Float, int, error) {
	return parseAddSub(s, pos)
}

func skipSpaces(s string, pos int) int {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	return pos
}

func parseAddSub(s string, pos int) (*big.Float, int, error) {
	left, pos, err := parseMulDiv(s, pos)
	if err != nil {
		return nil, pos, err
	}
	for {
		pos = skipSpaces(s, pos)
		if pos >= len(s) {
			break
		}
		op := s[pos]
		if op != '+' && op != '-' {
			break
		}
		pos++
		right, newPos, err := parseMulDiv(s, pos)
		if err != nil {
			return nil, newPos, err
		}
		pos = newPos
		if op == '+' {
			left = new(big.Float).Add(left, right)
		} else {
			left = new(big.Float).Sub(left, right)
		}
	}
	return left, pos, nil
}

func parseMulDiv(s string, pos int) (*big.Float, int, error) {
	left, pos, err := parseUnary(s, pos)
	if err != nil {
		return nil, pos, err
	}
	for {
		pos = skipSpaces(s, pos)
		if pos >= len(s) {
			break
		}
		op := s[pos]
		if op != '*' && op != '/' {
			break
		}
		pos++
		right, newPos, err := parseUnary(s, pos)
		if err != nil {
			return nil, newPos, err
		}
		pos = newPos
		if op == '*' {
			left = new(big.Float).Mul(left, right)
		} else {
			if right.Sign() == 0 {
				return nil, pos, fmt.Errorf("division by zero")
			}
			left = new(big.Float).Quo(left, right)
		}
	}
	return left, pos, nil
}

func parseUnary(s string, pos int) (*big.Float, int, error) {
	pos = skipSpaces(s, pos)
	if pos < len(s) && s[pos] == '-' {
		val, newPos, err := parseAtom(s, pos+1)
		if err != nil {
			return nil, newPos, err
		}
		return new(big.Float).Neg(val), newPos, nil
	}
	if pos < len(s) && s[pos] == '+' {
		return parseAtom(s, pos+1)
	}
	return parseAtom(s, pos)
}

func parseAtom(s string, pos int) (*big.Float, int, error) {
	pos = skipSpaces(s, pos)
	if pos >= len(s) {
		return nil, pos, fmt.Errorf("unexpected end of expression")
	}
	if s[pos] == '(' {
		val, newPos, err := parseExpr(s, pos+1)
		if err != nil {
			return nil, newPos, err
		}
		newPos = skipSpaces(s, newPos)
		if newPos >= len(s) || s[newPos] != ')' {
			return nil, newPos, fmt.Errorf("missing closing parenthesis")
		}
		return val, newPos + 1, nil
	}
	// Parse number.
	end := pos
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.' || s[end] == 'e' || s[end] == 'E' ||
		((s[end] == '+' || s[end] == '-') && end > pos && (s[end-1] == 'e' || s[end-1] == 'E'))) {
		end++
	}
	if end == pos {
		return nil, pos, fmt.Errorf("expected number at position %d, got %q", pos, string(s[pos]))
	}
	f, _, err := big.ParseFloat(s[pos:end], 10, 256, big.ToNearestEven)
	if err != nil {
		return nil, end, fmt.Errorf("invalid number %q: %w", s[pos:end], err)
	}
	return f, end, nil
}

// Ensure token package is used (needed for the import).
var _ = token.ADD

func (m *pipedModule) cmdCalc(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	expr := args
	if expr == "" {
		expr = pipeInput
	}
	if expr == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>calc &lt;expr&gt;</code>")
	}

	result, err := safeCalc(expr)
	if err != nil {
		return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ Calc error: %v", err))
	}
	return m.pipeEdit(ctx, ev, state, result)
}

// ---------- .sed ----------

func (m *pipedModule) cmdSed(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	// Parse s/from/to/[g]
	re := regexp.MustCompile(`^s/(.*?)/(.*?)/([gi]*)(.*)$`)
	match := re.FindStringSubmatch(args)
	if match == nil {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>sed s/from/to/[g]</code>")
	}
	from, to, flags, inlineText := match[1], match[2], match[3], strings.TrimSpace(match[4])

	text := inlineText
	if text == "" {
		text = pipeInput
	}
	if text == "" || from == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>sed s/from/to/[g] [text]</code>")
	}

	reFlags := ""
	if strings.Contains(flags, "i") {
		reFlags = "(?i)"
	}
	pattern, err := regexp.Compile(reFlags + regexp.QuoteMeta(from))
	if err != nil {
		return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ Regex error: %v", err))
	}

	var result string
	if strings.Contains(flags, "g") {
		result = pattern.ReplaceAllString(text, to)
	} else {
		result = pattern.ReplaceAllLiteralString(text, to)
		// Only first match.
		idx := pattern.FindStringIndex(text)
		if idx != nil {
			result = text[:idx[0]] + to + text[idx[1]:]
		} else {
			result = text
		}
	}

	return m.pipeEdit(ctx, ev, state, result)
}

// ---------- .strip ----------

func (m *pipedModule) cmdStrip(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	removeEmpty := false
	if strings.HasPrefix(args, "-e") {
		removeEmpty = true
		args = strings.TrimSpace(args[2:])
	}

	text := args
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>strip [-e] [text]</code>")
	}

	lines := strings.Split(text, "\n")
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if removeEmpty && l == "" {
			continue
		}
		out = append(out, l)
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(out, "\n"))
}

// ---------- .b64 ----------

func (m *pipedModule) cmdB64(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	decode := false
	if strings.HasPrefix(args, "-d") {
		decode = true
		args = strings.TrimSpace(args[2:])
	} else if strings.HasPrefix(args, "decode ") {
		decode = true
		args = strings.TrimSpace(args[7:])
	} else if strings.HasPrefix(args, "encode ") {
		args = strings.TrimSpace(args[7:])
	}

	text := args
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>b64 [-d] [text]</code> or <code>b64 encode|decode &lt;text&gt;</code>")
	}

	var result string
	if decode {
		b, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			// Try URL encoding.
			b, err = base64.URLEncoding.DecodeString(text)
			if err != nil {
				return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ Base64 decode error: %v", err))
			}
		}
		result = string(b)
	} else {
		result = base64.StdEncoding.EncodeToString([]byte(text))
	}

	return m.pipeEdit(ctx, ev, state, result)
}

// ---------- .jq (json) ----------

func (m *pipedModule) cmdJSON(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	raw := pipeInput
	if raw == "" {
		raw = args
		args = ""
	}

	keys := strings.Fields(args)

	if raw == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>jq [key1 key2...] [json]</code> (or pipe JSON)")
	}

	var data interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ JSON parse error: %v", err))
	}

	if len(keys) == 0 {
		// Pretty print.
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ JSON marshal error: %v", err))
		}
		return m.pipeEdit(ctx, ev, state, string(b))
	}

	// Extract keys.
	var values []string
	for _, key := range keys {
		val := jsonGet(data, key)
		switch v := val.(type) {
		case nil:
			values = append(values, "null")
		case string:
			values = append(values, v)
		default:
			b, _ := json.Marshal(v)
			values = append(values, string(b))
		}
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(values, " "))
}

// jsonGet extracts a value from parsed JSON by dotted path.
func jsonGet(data interface{}, dotPath string) interface{} {
	parts := strings.Split(dotPath, ".")
	cur := data
	for _, p := range parts {
		switch v := cur.(type) {
		case map[string]interface{}:
			cur = v[p]
		case []interface{}:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil
			}
			cur = v[idx]
		default:
			return nil
		}
	}
	return cur
}

// ---------- .sleep ----------

func (m *pipedModule) cmdSleep(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, _, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	if args == "" {
		return m.pipeEdit(ctx, ev, state, "❌ Usage: <code>sleep &lt;seconds&gt;</code>")
	}

	secs, err := strconv.ParseFloat(strings.TrimSpace(args), 64)
	if err != nil || secs < 0 {
		return m.pipeEdit(ctx, ev, state, "❌ Specify a non-negative number of seconds.")
	}
	if secs > 300 {
		return m.pipeEdit(ctx, ev, state, "❌ Maximum sleep is 300 seconds.")
	}

	time.Sleep(time.Duration(secs * float64(time.Second)))
	return m.pipeEdit(ctx, ev, state, "ok")
}

// ---------- .delete ----------

func (m *pipedModule) cmdDelete(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	return ev.Delete(ctx)
}
