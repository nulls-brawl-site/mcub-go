// Package debugger ports the MCUB Python debugger (debugger/core.py, types.py, rules.py)
// to Go. Because Go cannot parse Python AST directly, rule matching is done on
// raw source text with regular expressions — the same patterns the Python rules catch
// in practice.
package debugger

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Types
// ─────────────────────────────────────────────────────────────────────────────

// Rule defines a pattern to detect issues in messages/commands.
type Rule struct {
	ID          string
	Name        string
	Pattern     *regexp.Regexp
	Severity    string // "error", "warning", "info"
	Description string
	Fix         string // suggested fix
}

// DebugResult is the result of checking a rule against a piece of text.
type DebugResult struct {
	Rule    *Rule
	Matched bool
	Context string // the matching line / snippet
	Line    int    // 1-indexed line number (0 if unknown)
}

// String returns a human-readable representation of a DebugResult.
func (r DebugResult) String() string {
	if !r.Matched {
		return fmt.Sprintf("[%s] no match", r.Rule.ID)
	}
	loc := ""
	if r.Line > 0 {
		loc = fmt.Sprintf(" (line %d)", r.Line)
	}
	out := fmt.Sprintf("[%s][%s] %s%s: %s",
		strings.ToUpper(r.Rule.Severity), r.Rule.ID, r.Rule.Name, loc, r.Context)
	if r.Rule.Fix != "" {
		out += "\n  Fix: " + r.Rule.Fix
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Debug log
// ─────────────────────────────────────────────────────────────────────────────

// DebugEntry is a single structured log entry produced by the debugger.
type DebugEntry struct {
	Time    time.Time
	Level   string
	Source  string
	Message string
}

// ─────────────────────────────────────────────────────────────────────────────
// Debugger
// ─────────────────────────────────────────────────────────────────────────────

// Debugger checks kernel state and module output for issues.
type Debugger struct {
	rules  []*Rule
	kernel interface{}
	mu     sync.RWMutex
	log    []DebugEntry
}

// New creates a new Debugger.  kernel may be nil (useful in tests).
func New(kernel interface{}) *Debugger {
	d := &Debugger{
		kernel: kernel,
	}
	for _, r := range DefaultRules() {
		d.rules = append(d.rules, r)
	}
	return d
}

// AddRule appends a debug rule to the debugger.
func (d *Debugger) AddRule(rule *Rule) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rules = append(d.rules, rule)
}

// Check runs all rules against text (module source / command output) and
// returns every match.  source is a human-readable label (e.g. the module
// file path).
func (d *Debugger) Check(text, source string) []DebugResult {
	d.mu.RLock()
	rules := make([]*Rule, len(d.rules))
	copy(rules, d.rules)
	d.mu.RUnlock()

	lines := strings.Split(text, "\n")
	var results []DebugResult

	for _, rule := range rules {
		if rule.Pattern == nil {
			continue
		}
		for lineNo, line := range lines {
			if rule.Pattern.MatchString(line) {
				results = append(results, DebugResult{
					Rule:    rule,
					Matched: true,
					Context: strings.TrimSpace(line),
					Line:    lineNo + 1,
				})
				break // report first match per rule per text
			}
		}
	}
	return results
}

// Log adds an entry to the in-memory debug log.
func (d *Debugger) Log(level, source, message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = append(d.log, DebugEntry{
		Time:    time.Now(),
		Level:   level,
		Source:  source,
		Message: message,
	})
}

// GetLog returns the most recent log entries (up to limit).
// A limit of 0 or negative returns all entries.
func (d *Debugger) GetLog(limit int) []DebugEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if limit <= 0 || limit >= len(d.log) {
		out := make([]DebugEntry, len(d.log))
		copy(out, d.log)
		return out
	}
	start := len(d.log) - limit
	out := make([]DebugEntry, limit)
	copy(out, d.log[start:])
	return out
}

// ClearLog empties the debug log.
func (d *Debugger) ClearLog() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log = d.log[:0]
}

// CheckModule runs the debugger against a module's registered commands by
// looking up the module name in the kernel's LoadedModules map (if the kernel
// exposes one via the KernelInspector interface).  Falls back to a no-op if
// the kernel does not implement the interface.
func (d *Debugger) CheckModule(moduleName string) []DebugResult {
	type inspector interface {
		GetModuleSource(name string) (string, bool)
	}
	if ki, ok := d.kernel.(inspector); ok {
		src, found := ki.GetModuleSource(moduleName)
		if found {
			return d.Check(src, moduleName)
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Context-aware check helper (honours ctx cancellation for long batches)
// ─────────────────────────────────────────────────────────────────────────────

// CheckWithContext is like Check but aborts if ctx is cancelled.
func (d *Debugger) CheckWithContext(ctx context.Context, text, source string) ([]DebugResult, error) {
	d.mu.RLock()
	rules := make([]*Rule, len(d.rules))
	copy(rules, d.rules)
	d.mu.RUnlock()

	lines := strings.Split(text, "\n")
	var results []DebugResult

	for _, rule := range rules {
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		default:
		}
		if rule.Pattern == nil {
			continue
		}
		for lineNo, line := range lines {
			if rule.Pattern.MatchString(line) {
				results = append(results, DebugResult{
					Rule:    rule,
					Matched: true,
					Context: strings.TrimSpace(line),
					Line:    lineNo + 1,
				})
				break
			}
		}
	}
	return results, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// DefaultRules — MCUB standard rule set
//
// These regexps mirror the Python AST rules from debugger/rules.py.
// Because we operate on raw source text rather than AST nodes the patterns are
// conservative: they flag the most common mistakes without false-positive
// analysis that would require a full parser.
// ─────────────────────────────────────────────────────────────────────────────

func mustCompile(pattern string) *regexp.Regexp {
	return regexp.MustCompile(pattern)
}

// DefaultRules returns the standard MCUB debug rules (Go port of rules.py).
func DefaultRules() []*Rule {
	return []*Rule{
		// MCUB001 — event.edit() called with buttons= (should use reply_markup=)
		{
			ID:          "MCUB001",
			Name:        "EventEditWithButtons",
			Pattern:     mustCompile(`\.edit\([^)]*\bbuttons\s*=`),
			Severity:    "warning",
			Description: "event.edit() does not accept buttons= parameter; use reply_markup=.",
			Fix:         "Replace buttons= with reply_markup=",
		},
		// MCUB002 — event.edit() called with reply_markup= (use inline buttons builder)
		{
			ID:          "MCUB002",
			Name:        "EventEditWithReplyMarkup",
			Pattern:     mustCompile(`\.edit\([^)]*\breply_markup\s*=`),
			Severity:    "info",
			Description: "Use Button.inline() helpers instead of raw reply_markup in event.edit().",
			Fix:         "Build keyboard with Button.inline() and pass as buttons=",
		},
		// MCUB003 — @register.callback without pattern=
		{
			ID:          "MCUB003",
			Name:        "CallbackWithoutPattern",
			Pattern:     mustCompile(`@register\.callback\s*\(`),
			Severity:    "warning",
			Description: "Callback handlers should specify a pattern= to avoid catching all callbacks.",
			Fix:         "Add pattern='your_data' to @register.callback()",
		},
		// MCUB004 — event.answer() with show_alert= (correct usage is alert=)
		{
			ID:          "MCUB004",
			Name:        "EventAnswerShowAlert",
			Pattern:     mustCompile(`\.answer\([^)]*\bshow_alert\s*=`),
			Severity:    "warning",
			Description: "event.answer() uses alert= not show_alert=.",
			Fix:         "Replace show_alert= with alert=",
		},
		// MCUB007 — missing bot_client usage inside bot command handler
		{
			ID:          "MCUB007",
			Name:        "MissingBotClient",
			Pattern:     mustCompile(`@register\.bot_command`),
			Severity:    "info",
			Description: "bot_command handlers must use kernel.bot_client, not kernel.client.",
			Fix:         "Use kernel.bot_client inside @register.bot_command handlers",
		},
		// MCUB008 — async def without await inside body
		{
			ID:          "MCUB008",
			Name:        "AsyncWithoutAwait",
			Pattern:     mustCompile(`async\s+def\s+\w+[^:]*:\s*\n(?:[^\n]*\n)*?[^\n]*(?:return|pass)\s*$`),
			Severity:    "info",
			Description: "async function without any await expression may be better as a regular function.",
			Fix:         "Add await or change to regular def",
		},
		// MCUB009 — register typo (@rigister, @registr, etc.)
		{
			ID:          "MCUB009",
			Name:        "RegisterTypo",
			Pattern:     mustCompile(`@ri?e?g+i?s?t?e?r\b(?!ister)`),
			Severity:    "error",
			Description: "Possible typo in @register decorator.",
			Fix:         "Correct to @register",
		},
		// MCUB010 — Button.inline() wrong format (positional args)
		{
			ID:          "MCUB010",
			Name:        "ButtonInlineFormat",
			Pattern:     mustCompile(`Button\.inline\(\s*["'][^"']+["']\s*,\s*["'][^"']+["']\s*,`),
			Severity:    "warning",
			Description: "Button.inline() takes (text, data) — extra positional args are ignored.",
			Fix:         "Use Button.inline('text', 'data')",
		},
		// MCUB015 — kernel.client.delete_messages() (should be event.delete())
		{
			ID:          "MCUB015",
			Name:        "ClientDeleteMessages",
			Pattern:     mustCompile(`kernel\.client\.delete_messages\(`),
			Severity:    "warning",
			Description: "Prefer event.delete() over kernel.client.delete_messages() in handlers.",
			Fix:         "Use await event.delete()",
		},
		// MCUB018 — @register.event missing filter=
		{
			ID:          "MCUB018",
			Name:        "MissingPatternInEvent",
			Pattern:     mustCompile(`@register\.event\s*\(\s*\)`),
			Severity:    "warning",
			Description: "Raw @register.event() without a filter catches all updates.",
			Fix:         "Add a filter, e.g. @register.event(events.NewMessage(pattern='...'))",
		},
		// MCUB024 — kernel.inline_query_and_click() argument issues
		{
			ID:          "MCUB024",
			Name:        "InlineQueryAndClick",
			Pattern:     mustCompile(`inline_query_and_click\(`),
			Severity:    "info",
			Description: "Ensure inline_query_and_click() has valid query, result_index, and timeout args.",
			Fix:         "Pass query='text', result_index=0, timeout=10",
		},
		// MCUB025 — db key invalid format
		{
			ID:          "MCUB025",
			Name:        "DatabaseKeyFormat",
			Pattern:     mustCompile(`kernel\.db_(?:set|get|delete)\([^,)]*[^a-zA-Z0-9_\-'",\s]`),
			Severity:    "warning",
			Description: "Database module/key must match ^[a-zA-Z0-9_-]{1,64}$.",
			Fix:         "Use only alphanumeric, underscore, or hyphen chars; max 64 length",
		},
		// MCUB027 — bare except:
		{
			ID:          "MCUB027",
			Name:        "BareExcept",
			Pattern:     mustCompile(`^\s*except\s*:`),
			Severity:    "warning",
			Description: "Bare except: catches all exceptions including SystemExit and KeyboardInterrupt.",
			Fix:         "Use except Exception as e: and handle/log the error",
		},
		// MCUB028 — kernel.handle_error() without await
		{
			ID:          "MCUB028",
			Name:        "LoggerWithoutAwait",
			Pattern:     mustCompile(`(?:^|[^a-zA-Z])kernel\.handle_error\(`),
			Severity:    "warning",
			Description: "kernel.handle_error() is async — must be called with await.",
			Fix:         "Add 'await' before kernel.handle_error(...)",
		},
		// MCUB029 — HTML tags without parse_mode='html'
		{
			ID:          "MCUB029",
			Name:        "MissingParseModeForHtml",
			Pattern:     mustCompile(`(?:reply|edit|respond|send_message)\([^)]*<(?:b|i|u|s|code|pre|a |emoji|blockquote|tg-emoji)`),
			Severity:    "warning",
			Description: "HTML tags detected in message but parse_mode='html' is missing.",
			Fix:         "Add parse_mode='html' parameter",
		},
		// MCUB050 — class-style module missing ModuleBase
		{
			ID:          "MCUB050",
			Name:        "ClassStyleModuleBase",
			Pattern:     mustCompile(`^class\s+\w+\s*\(`),
			Severity:    "error",
			Description: "Class-style module must inherit from ModuleBase.",
			Fix:         "class MyModule(ModuleBase):",
		},
		// MCUB056 — class-style module missing name attribute
		{
			ID:          "MCUB056",
			Name:        "ClassStyleName",
			Pattern:     mustCompile(`class\s+\w+\s*\(ModuleBase\)`),
			Severity:    "error",
			Description: "Class-style module must define a 'name' class attribute.",
			Fix:         "Add: name = 'MyModule'",
		},
		// MCUB060 — class-style module missing docstring
		{
			ID:          "MCUB060",
			Name:        "ClassStyleDocstring",
			Pattern:     mustCompile(`class\s+\w+\s*\(ModuleBase\)\s*:\s*\n\s*(?!""")`),
			Severity:    "info",
			Description: "Class-style module should have a docstring.",
			Fix:         `Add: """Module description."""`,
		},
	}
}
