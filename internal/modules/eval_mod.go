// SPDX-License-Identifier: MIT
// Port of modules/eval.py from MCUB-fork.

package modules

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji IDs for the eval module (exact from Python source).
const (
	emojiEvalOrb     = `<tg-emoji emoji-id="5426900601101374618">🧿</tg-emoji>`
	emojiEvalError   = `<tg-emoji emoji-id="5388785832956016892">❌</tg-emoji>`
	emojiEvalDNA     = `<tg-emoji emoji-id="5368513458469878442">🧬</tg-emoji>`
	emojiEvalDiamond = `<tg-emoji emoji-id="5404366668635865453">💠</tg-emoji>`
)

// pyExecWrapper is the Python bootstrap code used to execute user code.
// It wraps the code in an async function, runs it with asyncio, and captures output.
const pyExecWrapper = `
import sys, asyncio, io, traceback

_code = sys.stdin.read()

_old_stdout = sys.stdout
_old_stderr = sys.stderr
sys.stdout = sys.stderr = _out = io.StringIO()

_result_str = ""
_is_error = False

try:
    _locals = {}
    _lines = _code.split("\n")
    _wrapped = "async def __exec():\n    " + "\n    ".join(_lines)
    exec(_wrapped, _locals)
    _res = asyncio.run(_locals["__exec"]())
    _complete = _out.getvalue()
    if _res is not None:
        _complete += str(_res)
    _result_str = _complete
except Exception:
    _complete = _out.getvalue() + traceback.format_exc()
    _result_str = _complete
    _is_error = True
finally:
    sys.stdout = _old_stdout
    sys.stderr = _old_stderr

sys.stdout.write(_result_str)
`

// evalModule implements the "eval" system module.
type evalModule struct {
	k *kernel.Kernel
}

func newEvalModule() loader.Module { return &evalModule{} }

// Name implements loader.Module.
func (m *evalModule) Name() string { return "eval" }

// OnLoad implements loader.Module.
func (m *evalModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("eval: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *evalModule) OnUnload(k interface{}) error {
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
func (m *evalModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "py", Description: "<code> execute Python code", Handler: m.cmdPy},
	}
}

// ---------- .py ----------

// cmdPy executes Python code via a subprocess and displays the result.
// Output format matches Python eval.py cmd_py exactly.
func (m *evalModule) cmdPy(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	// Get code after ".py" command.
	code := strings.TrimSpace(getArgsRaw(ev, m.k))
	// Unescape HTML entities (the message may contain &amp; etc.)
	code = html.UnescapeString(code)
	// Replace non-breaking spaces (matches Python: .replace("\u00a0", " "))
	code = strings.ReplaceAll(code, "\u00a0", " ")

	if code == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			emojiEvalError+" <b>No code provided.</b> Usage: <code>.py &lt;code&gt;</code>")
	}

	// Show executing indicator.
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		emojiEvalOrb+" <i>Executing...</i>"); err != nil {
		return err
	}

	start := time.Now()
	output, isError := m.execPython(ctx, code)
	elapsed := float64(time.Since(start).Microseconds()) / 1000.0

	if output == "" {
		output = "[no output]"
	}

	// Truncate code display: Python does code[:1000] FIRST (raw), then html.escape.
	// Matches: code_display = html.escape(code[:1000]) + ("..." if len(code) > 1000 else "")
	codeRaw := code
	suffix := ""
	if len([]rune(codeRaw)) > 1000 {
		runes := []rune(codeRaw)
		codeRaw = string(runes[:1000])
		suffix = "..."
	}
	codeDisplay := html.EscapeString(codeRaw) + suffix

	// Elapsed string: Python uses f"{elapsed}{s['ms']}" – NO space between number and unit
	elapsedStr := fmt.Sprintf("%.2f", elapsed)
	msLabel := s(m.k, "eval", "ms") // "ms" or "мс"

	// result_text for display
	resultText := output
	if len(resultText) > 4000 {
		// Send result as a file (matches Python: len(result_text) > 4000)
		return m.sendResultAsFile(ctx, ev, code, output, codeDisplay, elapsedStr, msLabel)
	}

	// Build result block:
	// If error: <blockquote expandable>{result_text}</blockquote>  (no <code>, since may contain HTML)
	// If ok:    <blockquote expandable><code>{html.escape(result_text)}</code></blockquote>
	var resultBlock string
	if isError {
		// Python puts pre-formatted HTML (with ❌) in result_text for errors,
		// so it's used raw. In Go, output is plain text traceback – escape it.
		resultBlock = fmt.Sprintf("<blockquote expandable>%s</blockquote>",
			html.EscapeString(resultText))
	} else {
		resultBlock = fmt.Sprintf("<blockquote expandable><code>%s</code></blockquote>",
			html.EscapeString(resultText))
	}

	// Python format (result in message):
	// f"""{🧿} <b>{s["code"]}</b>
	// <blockquote expandable><code>{code_display}</code></blockquote>
	// {🧬} <b>{s["result_in_message"]}</b>
	// {result_block}
	// <blockquote>{💠} <i>{s["executed_in"]}</i> <code>{elapsed}{ms}</code></blockquote>"""
	response := fmt.Sprintf(
		"%s <b>%s</b>\n"+
			"<blockquote expandable><code>%s</code></blockquote>\n"+
			"%s <b>%s</b>\n"+
			"%s\n"+
			"<blockquote>%s <i>%s</i> <code>%s%s</code></blockquote>",
		emojiEvalOrb, s(m.k, "eval", "code"),
		codeDisplay,
		emojiEvalDNA, s(m.k, "eval", "result_in_message"),
		resultBlock,
		emojiEvalDiamond, s(m.k, "eval", "executed_in"), elapsedStr, msLabel,
	)

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, response)
}

// sendResultAsFile sends the eval result as a document attachment when it exceeds 4000 chars.
// Matches Python: creates BytesIO file named "eval_result.txt" and edits message with file.
func (m *evalModule) sendResultAsFile(
	ctx context.Context,
	ev *events.NewMessage,
	code, output, codeDisplay, elapsedStr, msLabel string,
) error {
	// Write content to temp file
	tmp, err := os.CreateTemp("", "eval_result_*.txt")
	if err != nil {
		// Fallback: show caption without file
		caption := fmt.Sprintf(
			"%s <b>%s</b>\n"+
				"<blockquote expandable><code>%s</code></blockquote>\n"+
				"%s <b>%s</b>\n"+
				"<blockquote>%s <i>%s</i> <code>%s%s</code></blockquote>",
			emojiEvalOrb, s(m.k, "eval", "code"),
			codeDisplay,
			emojiEvalDNA, s(m.k, "eval", "result_file"),
			emojiEvalDiamond, s(m.k, "eval", "executed_in"), elapsedStr, msLabel,
		)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, caption)
	}
	defer os.Remove(tmp.Name())

	_, _ = tmp.WriteString(output)
	_ = tmp.Close()

	// Caption matches Python format:
	// f"""{🧿} <b>{s["code"]}</b>
	// <blockquote expandable><code>{code_display}</code></blockquote>
	// {🧬} <b>{s["result_file"]}</b>
	// <blockquote>{💠} <i>{s["executed_in"]}</i> <code>{elapsed}{ms}</code></blockquote>"""
	caption := fmt.Sprintf(
		"%s <b>%s</b>\n"+
			"<blockquote expandable><code>%s</code></blockquote>\n"+
			"%s <b>%s</b>\n"+
			"<blockquote>%s <i>%s</i> <code>%s%s</code></blockquote>",
		emojiEvalOrb, s(m.k, "eval", "code"),
		codeDisplay,
		emojiEvalDNA, s(m.k, "eval", "result_file"),
		emojiEvalDiamond, s(m.k, "eval", "executed_in"), elapsedStr, msLabel,
	)

	// Try to send as document; on failure, fall back to edit without file
	if err := sendDocument(ctx, m.k, ev.PeerID, tmp.Name(), caption); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, caption)
	}
	return nil
}

// execPython runs the given Python code via a python3 subprocess.
// Returns the captured output and whether an error occurred.
func (m *evalModule) execPython(ctx context.Context, code string) (output string, isError bool) {
	cmd := exec.CommandContext(ctx, "python3", "-c", pyExecWrapper)
	cmd.Stdin = strings.NewReader(code)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	result := stdout.String()
	if result == "" && stderr.Len() > 0 {
		result = stderr.String()
		isError = true
	} else if err != nil {
		isError = true
	}

	return result, isError
}
