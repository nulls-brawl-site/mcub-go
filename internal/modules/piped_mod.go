package modules

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// pipedModule provides piped/pipeline utility commands (port of utils-piped.py).
type pipedModule struct {
	k        *kernel.Kernel
	pipeVars sync.Map // export/import variable storage
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

// Commands implements loader.Module – all 24 piped commands.
func (m *pipedModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "echo", Description: "[text] — echo text (supports pipe_input)", Handler: m.cmdEcho},
		{Name: "grep", Description: "[-v] [-r] <pattern> [text] — filter lines", Handler: m.cmdGrep},
		{Name: "head", Description: "[-N] [text] — first N lines (default 10)", Handler: m.cmdHead},
		{Name: "tail", Description: "[-N] [text] — last N lines (default 10)", Handler: m.cmdTail},
		{Name: "sort", Description: "[-r] [-u] [text] — sort lines", Handler: m.cmdSort},
		{Name: "uniq", Description: "[-c] [text] — remove duplicate lines", Handler: m.cmdUniq},
		{Name: "wc", Description: "[-l|-w|-c] [text] — count lines/words/chars", Handler: m.cmdWC},
		{Name: "calc", Description: "<expr> — calculate math expression", Handler: m.cmdCalc},
		{Name: "sed", Description: "[-r] s/from/to/[gi] [text] — text substitution", Handler: m.cmdSed},
		{Name: "strip", Description: "[-e] [text] — strip whitespace from each line", Handler: m.cmdStrip},
		{Name: "b64", Description: "[-d] [text] — base64 encode/decode", Handler: m.cmdB64},
		{Name: "jq", Description: "[key...] — format JSON / extract fields", Handler: m.cmdJSON},
		{Name: "sleep", Description: "<seconds> — sleep N seconds", Handler: m.cmdSleep},
		{Name: "delete", Description: "— delete the event message", Handler: m.cmdDelete},
		{Name: "random", Description: "[-l] [N [M]] — random number or random line", Handler: m.cmdRandom},
		{Name: "fwd", Description: "<N> [delay] — resend replied message N times", Handler: m.cmdFwd},
		{Name: "export", Description: "<name> [text] — save text to pipe variable", Handler: m.cmdExport},
		{Name: "import", Description: "<name> — load pipe variable into pipe", Handler: m.cmdImport},
		{Name: "get_reply", Description: "[text|id|sender|chat|date|media] — get reply message data", Handler: m.cmdGetReply},
		{Name: "repeat", Description: "<N> [text] — repeat text N times", Handler: m.cmdRepeat},
		{Name: "nop", Description: "— no operation (pass through)", Handler: m.cmdNop},
		{Name: "if", Description: "<pattern> [text] — pass through if pattern matches", Handler: m.cmdIf},
		{Name: "open", Description: "<path> — read file into pipe", Handler: m.cmdOpen},
		{Name: "write", Description: "[-n] <path> [text] — write to file", Handler: m.cmdWrite},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// pipeState extracts the pipeline capture state from the context.
func pipeState(ctx context.Context) (state *pybridge.PipelineCaptureState, pipeInput string, isPiped bool) {
	state = pybridge.PipelineCaptureFromContext(ctx)
	if state != nil {
		pipeInput = state.Input
		isPiped = state.IsPiped
	}
	return
}

// pipeEdit edits the message and records the output in the pipeline state.
func (m *pipedModule) pipeEdit(ctx context.Context, ev *events.NewMessage, state *pybridge.PipelineCaptureState, text string) error {
	if state != nil {
		state.Capture(text)
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, text)
}

// argsRaw returns the raw text after the command word.
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

// ---------------------------------------------------------------------------
// .echo
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// .nop
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdNop(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, isPiped := pipeState(ctx)
	if isPiped {
		return m.pipeEdit(ctx, ev, state, pipeInput)
	}
	return m.pipeEdit(ctx, ev, state, "")
}

// ---------------------------------------------------------------------------
// .delete
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdDelete(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	return ev.Delete(ctx)
}

// ---------------------------------------------------------------------------
// .grep
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdGrep(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	invert := false
	useRegex := false
	showLineNumbers := false

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
		if strings.Contains(flag, "l") {
			showLineNumbers = true
		}
	}

	pattern, inlineText := "", ""
	if args != "" {
		if args[0] == '\'' || args[0] == '"' {
			quote := args[0]
			end := strings.IndexByte(args[1:], quote)
			if end >= 0 {
				pattern = args[1 : end+1]
				inlineText = strings.TrimSpace(args[end+2:])
			} else {
				pattern = args[1:]
			}
		} else {
			parts := strings.SplitN(args, " ", 2)
			pattern = parts[0]
			if len(parts) > 1 {
				inlineText = parts[1]
			}
		}
	}

	text := inlineText
	if text == "" {
		text = pipeInput
	}
	if pattern == "" || text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ "+m.k.Prefix()+"grep [-v] [-r] &lt;pattern&gt; [text]")
	}

	lines := strings.Split(text, "\n")
	var matched []string
	for i, line := range lines {
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
			if showLineNumbers {
				matched = append(matched, fmt.Sprintf("%d: %s", i+1, line))
			} else {
				matched = append(matched, line)
			}
		}
	}

	if len(matched) == 0 {
		return m.pipeEdit(ctx, ev, state, "Not found")
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(matched, "\n"))
}

// ---------------------------------------------------------------------------
// .head
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ head [-N] [text]")
	}

	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(lines, "\n"))
}

// ---------------------------------------------------------------------------
// .tail
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ tail [-N] [text]")
	}

	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(lines, "\n"))
}

// ---------------------------------------------------------------------------
// .sort
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ sort [-r] [-u] [text]")
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

// ---------------------------------------------------------------------------
// .uniq
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdUniq(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	countMode := false
	if strings.HasPrefix(args, "-c") {
		countMode = true
		args = strings.TrimSpace(args[2:])
	}

	text := args
	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ uniq [-c] [text]")
	}

	lines := strings.Split(text, "\n")
	var out []string

	if countMode {
		// Group consecutive identical lines and count them.
		i := 0
		for i < len(lines) {
			j := i + 1
			for j < len(lines) && lines[j] == lines[i] {
				j++
			}
			out = append(out, fmt.Sprintf("%d %s", j-i, lines[i]))
			i = j
		}
	} else {
		seen := map[string]bool{}
		for _, l := range lines {
			if !seen[l] {
				seen[l] = true
				out = append(out, l)
			}
		}
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(out, "\n"))
}

// ---------------------------------------------------------------------------
// .wc
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ wc [-l|-w|-c] [text]")
	}

	var result string
	switch mode {
	case "w":
		result = strconv.Itoa(len(strings.Fields(text)))
	case "c":
		result = strconv.Itoa(len(text))
	default: // "l"
		result = strconv.Itoa(len(strings.Split(text, "\n")))
	}
	return m.pipeEdit(ctx, ev, state, result)
}

// ---------------------------------------------------------------------------
// .calc — recursive descent arithmetic parser
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ calc &lt;expr&gt;")
	}

	// If expr starts with an operator and pipeInput is a number, apply to it.
	if len(expr) > 0 && (expr[0] == '+' || expr[0] == '-' || expr[0] == '*' || expr[0] == '/') {
		if num, err := new(big.Float).SetPrec(256).SetString(strings.TrimSpace(pipeInput)); err {
			opExpr := fmt.Sprintf("(%s)%s", strings.TrimSpace(pipeInput), expr)
			result, calcErr := safeCalc(opExpr)
			if calcErr != nil {
				return m.pipeEdit(ctx, ev, state, "❌ Calc error: "+calcErr.Error())
			}
			return m.pipeEdit(ctx, ev, state, result)
		} else {
			_ = num
		}
	}

	result, err := safeCalc(expr)
	if err != nil {
		return m.pipeEdit(ctx, ev, state, "❌ Calc error: "+err.Error())
	}
	return m.pipeEdit(ctx, ev, state, result)
}

// safeCalc evaluates a simple arithmetic expression using big.Float.
func safeCalc(expr string) (string, error) {
	val, _, err := parseExpr(strings.TrimSpace(expr), 0)
	if err != nil {
		return "", err
	}
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
			left = new(big.Float).SetPrec(256).Add(left, right)
		} else {
			left = new(big.Float).SetPrec(256).Sub(left, right)
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
			left = new(big.Float).SetPrec(256).Mul(left, right)
		} else {
			if right.Sign() == 0 {
				return nil, pos, fmt.Errorf("division by zero")
			}
			left = new(big.Float).SetPrec(256).Quo(left, right)
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
		return new(big.Float).SetPrec(256).Neg(val), newPos, nil
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

// ---------------------------------------------------------------------------
// .sed
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdSed(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	useRegex := false
	if strings.HasPrefix(args, "-r ") || args == "-r" {
		useRegex = true
		args = strings.TrimSpace(args[2:])
	}

	re := regexp.MustCompile(`^s/(.*?)/(.*?)/([gi]*)(.*)$`)
	match := re.FindStringSubmatch(args)
	if match == nil {
		return m.pipeEdit(ctx, ev, state, "❌ sed [-r] s/from/to/[gi] [text]")
	}
	from, to, flags, inlineText := match[1], match[2], match[3], strings.TrimSpace(match[4])

	text := inlineText
	if text == "" {
		text = pipeInput
	}
	if text == "" || from == "" {
		return m.pipeEdit(ctx, ev, state, "❌ sed s/from/to/[gi] [text]")
	}

	reFlags := ""
	if strings.Contains(flags, "i") {
		reFlags = "(?i)"
	}

	var patStr string
	if useRegex {
		patStr = reFlags + from
	} else {
		patStr = reFlags + regexp.QuoteMeta(from)
	}

	pat, err := regexp.Compile(patStr)
	if err != nil {
		return m.pipeEdit(ctx, ev, state, "❌ Regex error: "+err.Error())
	}

	var result string
	if strings.Contains(flags, "g") {
		result = pat.ReplaceAllString(text, to)
	} else {
		idx := pat.FindStringIndex(text)
		if idx != nil {
			result = text[:idx[0]] + to + text[idx[1]:]
		} else {
			result = text
		}
	}
	return m.pipeEdit(ctx, ev, state, result)
}

// ---------------------------------------------------------------------------
// .strip
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ strip [-e] [text]")
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

// ---------------------------------------------------------------------------
// .b64
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ b64 [-d] [text]")
	}

	var result string
	if decode {
		b, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			b, err = base64.URLEncoding.DecodeString(text)
			if err != nil {
				return m.pipeEdit(ctx, ev, state, "❌ Base64 decode error: "+err.Error())
			}
		}
		result = string(b)
	} else {
		result = base64.StdEncoding.EncodeToString([]byte(text))
	}
	return m.pipeEdit(ctx, ev, state, result)
}

// ---------------------------------------------------------------------------
// .jq (json)
// ---------------------------------------------------------------------------

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
		return m.pipeEdit(ctx, ev, state, "❌ jq [key1 key2...] — pipe JSON or provide inline")
	}

	var data interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return m.pipeEdit(ctx, ev, state, "❌ JSON parse error: "+err.Error())
	}

	if len(keys) == 0 {
		b, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			return m.pipeEdit(ctx, ev, state, "❌ JSON marshal error: "+err.Error())
		}
		return m.pipeEdit(ctx, ev, state, string(b))
	}

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

// ---------------------------------------------------------------------------
// .sleep
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdSleep(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, _, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	if args == "" {
		return m.pipeEdit(ctx, ev, state, "❌ sleep &lt;seconds&gt;")
	}

	secs, err := strconv.ParseFloat(strings.TrimSpace(args), 64)
	if err != nil || secs < 0 {
		return m.pipeEdit(ctx, ev, state, "❌ Specify a non-negative number of seconds.")
	}
	if secs > 300 {
		return m.pipeEdit(ctx, ev, state, "❌ Maximum sleep is 300 seconds.")
	}

	select {
	case <-ctx.Done():
	case <-time.After(time.Duration(secs * float64(time.Second))):
	}
	return m.pipeEdit(ctx, ev, state, "ok")
}

// ---------------------------------------------------------------------------
// .random
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdRandom(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := m.argsRaw(ev)

	pickLine := false
	lineRe := regexp.MustCompile(`(?:^|\s)-l(?:\s|$)`)
	if lineRe.MatchString(args) {
		pickLine = true
		args = strings.TrimSpace(lineRe.ReplaceAllString(args, " "))
	}

	var result string

	if pickLine {
		text := args
		if text == "" {
			text = pipeInput
		}
		if text == "" {
			return m.pipeEdit(ctx, ev, state, "❌ random -l [text]")
		}
		var lines []string
		for _, l := range strings.Split(text, "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			return m.pipeEdit(ctx, ev, state, "Not found")
		}
		result = lines[rand.Intn(len(lines))] // #nosec G404
	} else {
		parts := strings.Fields(args)
		lo, hi := 0, 100
		switch len(parts) {
		case 0:
			// defaults
		case 1:
			n, err := strconv.Atoi(parts[0])
			if err != nil {
				return m.pipeEdit(ctx, ev, state, "❌ random [-l] [N [M]]")
			}
			hi = n
		default:
			n1, err1 := strconv.Atoi(parts[0])
			n2, err2 := strconv.Atoi(parts[1])
			if err1 != nil || err2 != nil {
				return m.pipeEdit(ctx, ev, state, "❌ random [-l] [N [M]]")
			}
			lo, hi = n1, n2
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		result = strconv.Itoa(lo + rand.Intn(hi-lo+1)) // #nosec G404
	}
	return m.pipeEdit(ctx, ev, state, result)
}

// ---------------------------------------------------------------------------
// .fwd — resend replied message N times (without forward attribution)
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdFwd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, _, _ := pipeState(ctx)

	if ev.ReplyToMsgID == 0 {
		return m.pipeEdit(ctx, ev, state, "❌ Reply to a message first")
	}

	args := strings.Fields(m.argsRaw(ev))
	n := 1
	delay := 0.0

	if len(args) >= 1 {
		if num, err := strconv.Atoi(args[0]); err == nil {
			n = num
		}
	}
	if len(args) >= 2 {
		if d, err := strconv.ParseFloat(args[1], 64); err == nil {
			delay = d
		}
	}

	if n < 1 || n > 200 {
		return m.pipeEdit(ctx, ev, state, "❌ N must be between 1 and 200")
	}

	// Fetch the replied message.
	msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
	if err != nil || len(msgs) == 0 || msgs[0] == nil {
		return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ Failed to get message: %v", err))
	}
	msg := msgs[0]

	// Show progress.
	_ = m.pipeEdit(ctx, ev, state, fmt.Sprintf("Forwarding %d time(s)...", n))

	sent := 0
	for i := 0; i < n; i++ {
		var sendErr error
		if msg.Media != nil {
			// Has media: forward using ForwardMessage.
			sendErr = m.k.Client.ForwardMessage(ctx, ev.PeerID, ev.PeerID, ev.ReplyToMsgID)
		} else {
			// Text-only: resend content without attribution.
			sendErr = sendHTML(ctx, m.k, ev.PeerID, msg.Message)
		}
		if sendErr == nil {
			sent++
		}
		if delay > 0 && i < n-1 {
			select {
			case <-ctx.Done():
				break
			case <-time.After(time.Duration(delay * float64(time.Second))):
			}
		}
	}
	return m.pipeEdit(ctx, ev, state, fmt.Sprintf("Done: %d/%d sent", sent, n))
}

// ---------------------------------------------------------------------------
// .export — save text to named pipe variable
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdExport(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := strings.TrimSpace(m.argsRaw(ev))

	if args == "" && pipeInput == "" {
		return m.pipeEdit(ctx, ev, state, "❌ export &lt;name&gt; [text]")
	}

	if args == "" {
		// No name given; just pass through.
		return m.pipeEdit(ctx, ev, state, pipeInput)
	}

	parts := strings.SplitN(args, " ", 2)
	name := parts[0]
	var value string
	if len(parts) > 1 {
		value = parts[1]
	} else {
		value = pipeInput
	}

	if value == "" {
		return m.pipeEdit(ctx, ev, state, "❌ export &lt;name&gt; [text]")
	}

	m.pipeVars.Store(name, value)
	return m.pipeEdit(ctx, ev, state, fmt.Sprintf("exported: %s", name))
}

// ---------------------------------------------------------------------------
// .import — load named pipe variable
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdImport(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, _, _ := pipeState(ctx)
	args := strings.TrimSpace(m.argsRaw(ev))

	if args == "" {
		return m.pipeEdit(ctx, ev, state, "❌ import &lt;name&gt;")
	}

	name := strings.Fields(args)[0]
	raw, ok := m.pipeVars.Load(name)
	if !ok {
		return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ not found: %s", name))
	}
	return m.pipeEdit(ctx, ev, state, raw.(string))
}

// ---------------------------------------------------------------------------
// .get_reply — get data from the replied message
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdGetReply(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, _, _ := pipeState(ctx)
	args := strings.ToLower(strings.TrimSpace(m.argsRaw(ev)))

	if ev.ReplyToMsgID == 0 {
		return m.pipeEdit(ctx, ev, state, "❌ No reply message")
	}

	msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
	if err != nil || len(msgs) == 0 || msgs[0] == nil {
		return m.pipeEdit(ctx, ev, state, "❌ No reply message")
	}
	msg := msgs[0]

	var result string
	switch args {
	case "id":
		result = strconv.Itoa(msg.ID)
	case "sender":
		if msg.FromID != nil {
			switch peer := msg.FromID.(type) {
			case *tg.PeerUser:
				result = strconv.FormatInt(peer.UserID, 10)
			case *tg.PeerChannel:
				result = strconv.FormatInt(peer.ChannelID, 10)
			case *tg.PeerChat:
				result = strconv.FormatInt(peer.ChatID, 10)
			}
		}
	case "chat":
		result = strconv.FormatInt(ev.PeerID, 10)
	case "date":
		result = strconv.FormatInt(int64(msg.Date), 10)
	case "media":
		if msg.Media != nil {
			result = "true"
		} else {
			result = "false"
		}
	default: // "text" or empty
		result = msg.Message
	}
	return m.pipeEdit(ctx, ev, state, result)
}

// ---------------------------------------------------------------------------
// .repeat — repeat text N times
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdRepeat(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := strings.TrimSpace(m.argsRaw(ev))

	if args == "" {
		return m.pipeEdit(ctx, ev, state, "❌ repeat &lt;N&gt; [text]")
	}

	parts := strings.SplitN(args, " ", 3)
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return m.pipeEdit(ctx, ev, state, "❌ repeat &lt;N&gt; [text]")
	}
	if n < 1 || n > 100 {
		return m.pipeEdit(ctx, ev, state, "❌ N must be between 1 and 100")
	}

	sep := "\n"
	text := ""

	if len(parts) >= 2 {
		// Check for quoted separator.
		rest := strings.Join(parts[1:], " ")
		if len(rest) > 0 && (rest[0] == '\'' || rest[0] == '"') {
			quote := rest[0]
			end := strings.IndexByte(rest[1:], quote)
			if end >= 0 {
				sep = rest[1 : end+1]
				text = strings.TrimSpace(rest[end+2:])
			} else {
				text = rest
			}
		} else {
			text = rest
		}
	}

	if text == "" {
		text = pipeInput
	}
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ repeat &lt;N&gt; [text]")
	}

	repeated := make([]string, n)
	for i := range repeated {
		repeated[i] = text
	}
	return m.pipeEdit(ctx, ev, state, strings.Join(repeated, sep))
}

// ---------------------------------------------------------------------------
// .if — pass through if pattern matches, else stop pipeline
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdIf(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := strings.TrimSpace(m.argsRaw(ev))

	if args == "" {
		return m.pipeEdit(ctx, ev, state, "❌ if &lt;pattern&gt; [text]")
	}

	var pattern, inlineText string
	if args[0] == '\'' || args[0] == '"' {
		quote := args[0]
		end := strings.IndexByte(args[1:], quote)
		if end >= 0 {
			pattern = args[1 : end+1]
			inlineText = strings.TrimSpace(args[end+2:])
		} else {
			pattern = args[1:]
		}
	} else {
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
	if text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ if &lt;pattern&gt; [text]")
	}

	re, err := regexp.Compile(pattern)
	var matched bool
	if err != nil {
		matched = strings.Contains(text, pattern)
	} else {
		matched = re.MatchString(text)
	}

	if matched {
		return m.pipeEdit(ctx, ev, state, text)
	}
	return m.pipeEdit(ctx, ev, state, "No match - pipeline stopped")
}

// ---------------------------------------------------------------------------
// .open — read a file into the pipe
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdOpen(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, isPiped := pipeState(ctx)
	args := strings.TrimSpace(m.argsRaw(ev))

	filePath := args
	if filePath == "" {
		filePath = strings.TrimSpace(pipeInput)
	}
	if filePath == "" {
		return m.pipeEdit(ctx, ev, state, "❌ open &lt;path&gt;")
	}

	// Allow both absolute and relative paths.
	content, err := os.ReadFile(filePath) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ File not found: %s", filePath))
		}
		return m.pipeEdit(ctx, ev, state, fmt.Sprintf("❌ Error: %v", err))
	}

	text := string(content)
	if isPiped {
		return m.pipeEdit(ctx, ev, state, text)
	}
	lines := strings.Split(text, "\n")
	info := fmt.Sprintf("<b>%s</b>\n%d lines | %d bytes",
		filePath, len(lines), len(content))
	return m.pipeEdit(ctx, ev, state, info)
}

// ---------------------------------------------------------------------------
// .write — write text to a file
// ---------------------------------------------------------------------------

func (m *pipedModule) cmdWrite(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	state, pipeInput, _ := pipeState(ctx)
	args := strings.TrimSpace(m.argsRaw(ev))

	append_ := false
	if strings.HasPrefix(args, "-n ") || args == "-n" {
		append_ = true
		args = strings.TrimSpace(args[2:])
	}

	if args == "" {
		return m.pipeEdit(ctx, ev, state, "❌ write [-n] &lt;path&gt; [text]")
	}

	parts := strings.SplitN(args, " ", 2)
	path := parts[0]
	var text string
	if len(parts) > 1 {
		text = parts[1]
	} else {
		text = pipeInput
	}

	if path == "" || text == "" {
		return m.pipeEdit(ctx, ev, state, "❌ write [-n] &lt;path&gt; [text]")
	}

	// Create parent directories.
	if dir := strings.LastIndex(path, "/"); dir > 0 {
		if err := os.MkdirAll(path[:dir], 0o755); err != nil {
			return m.pipeEdit(ctx, ev, state, "❌ Write error: "+err.Error())
		}
	}

	var f *os.File
	var openErr error
	if append_ {
		f, openErr = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) // #nosec G304
	} else {
		f, openErr = os.Create(path) // #nosec G304
	}
	if openErr != nil {
		return m.pipeEdit(ctx, ev, state, "❌ Write error: "+openErr.Error())
	}
	defer f.Close()

	if _, err := f.WriteString(text); err != nil {
		return m.pipeEdit(ctx, ev, state, "❌ Write error: "+err.Error())
	}
	return m.pipeEdit(ctx, ev, state, fmt.Sprintf("Written: %s", path))
}
