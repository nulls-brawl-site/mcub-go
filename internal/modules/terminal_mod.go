// SPDX-License-Identifier: MIT
// Port of terminal.py → Go system module

package modules

import (
	"bufio"
	"context"
	"fmt"
	"html"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// ansiRe matches ANSI terminal escape sequences.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[mABCDEFGHJKSTfhilmnprsu]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// filterProxychains removes lines containing "[proxychains]" markers (default: on).
func filterProxychains(text string) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, line := range lines {
		if !strings.Contains(line, "[proxychains]") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// Custom emoji IDs matching Python's CUSTOM_EMOJI dict exactly.
const (
	// loading indicator (🔘)
	termEmojiLoading = `<tg-emoji emoji-id="5310041868191407556">🔘</tg-emoji>`
	// done (☑️) – used when exit code == 0
	termEmojiDone = `<tg-emoji emoji-id="5332533929020761310">☑️</tg-emoji>`
	// done_error_code (😖) – used when exit code != 0
	termEmojiDoneError = `<tg-emoji emoji-id="5330273431898318607">😖</tg-emoji>`
	// 🗯 – warning/error speech bubble (launch error, already running, no running, stop error)
	termEmojiSpeech = `<tg-emoji emoji-id="5465132703458270101">🗯</tg-emoji>`
	// 💬 – info speech bubble (already completed)
	termEmojiInfoBubble = `<tg-emoji emoji-id="5465300082628763143">💬</tg-emoji>`
	// ☑️ – check box (command stopped, stdin success); different emoji-id from termEmojiDone
	termEmojiCheckBox = `<tg-emoji emoji-id="5454096630372379732">☑️</tg-emoji>`
	// ⚠️ – warning triangle (stdin closed)
	termEmojiWarning = `<tg-emoji emoji-id="5453943626921666997">⚠️</tg-emoji>`
	// ❄️ – snowflake (executing)
	termEmojiSnowflake = `<tg-emoji emoji-id="5431895003821513760">❄️</tg-emoji>`
	// 📰 – newspaper (exit code label)
	termEmojiNewspaper = `<tg-emoji emoji-id="5433982607035474385">📰</tg-emoji>`
	// 🧮 – abacus (time display)
	termEmojiAbacus = `<tg-emoji emoji-id="5472404950673791399">🧮</tg-emoji>`
	// 🉐 – good luck / shell label
	termEmojiGoodLuck = `<tg-emoji emoji-id="5470088387048266598">🉐</tg-emoji>`
)

// parseSlot extracts an optional "@N" slot prefix from text.
// "@2 ls -la" → ("2", "ls -la"); "ls -la" → ("1", "ls -la"); "@all" → ("all", "").
func parseSlot(text string) (slot, cmd string) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "@") {
		parts := strings.SplitN(text, " ", 2)
		slot = parts[0][1:]
		if len(parts) > 1 {
			cmd = strings.TrimSpace(parts[1])
		}
		return slot, cmd
	}
	return "1", text
}

// runningCmd holds state for one executing shell command.
type runningCmd struct {
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdoutPipe io.ReadCloser
	stderrPipe io.ReadCloser
	stdout     strings.Builder
	stderr     strings.Builder
	done       bool
	exitCode   int
	msgID      int
	chatID     int64
	slot       string
	startTime  time.Time
	stdin      io.WriteCloser
	command    string
	pid        int
}

// terminalModule implements .t / .tkill / .ti.
type terminalModule struct {
	k       *kernel.Kernel
	running map[string]*runningCmd // key: "chatID:slot"
	mu      sync.Mutex
}

func newTerminalModule() loader.Module { return &terminalModule{} }

func (m *terminalModule) Name() string { return "terminal" }

func (m *terminalModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("terminal: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	m.running = make(map[string]*runningCmd)
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

func (m *terminalModule) OnUnload(k interface{}) error {
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

func (m *terminalModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "t", Description: "[@N] <command> execute shell command (optional slot @1-@N)", Handler: m.cmdT},
		{Name: "tkill", Description: "[@N|@all] stop running terminal command(s)", Handler: m.cmdTkill},
		{Name: "ti", Description: "[@N] <text> send text to stdin of a running command", Handler: m.cmdTi},
	}
}

// ---------- helpers ----------

func (m *terminalModule) slotKey(chatID int64, slot string) string {
	return fmt.Sprintf("%d:%s", chatID, slot)
}

// formatOutput escapes and truncates output for display inside <pre>.
// Returns the lang "empty" string when text is blank.
// Matches Python's _format_output exactly (truncate first, then html.escape).
func (m *terminalModule) formatOutput(text string) string {
	if strings.TrimSpace(text) == "" {
		return s(m.k, "terminal", "empty")
	}
	const maxLen = 2000
	if len(text) > maxLen {
		text = "...\n" + text[len(text)-maxLen:]
	}
	return html.EscapeString(text)
}

// buildMessage formats the running/final message to match Python _build_message exactly.
func (m *terminalModule) buildMessage(rc *runningCmd, final bool) string {
	rc.mu.Lock()
	stdout := rc.stdout.String()
	stderr := rc.stderr.String()
	done := rc.done
	exitCode := rc.exitCode
	startTime := rc.startTime
	slot := rc.slot
	cmdText := rc.command
	rc.mu.Unlock()

	// Apply output filters (defaults: strip ANSI + proxychains)
	stdout = stripANSI(stdout)
	stderr = stripANSI(stderr)
	stdout = filterProxychains(stdout)
	stderr = filterProxychains(stderr)

	stdoutBlock := fmt.Sprintf("<pre>%s</pre>", m.formatOutput(stdout))
	stderrBlock := ""
	if strings.TrimSpace(stderr) != "" {
		stderrBlock = fmt.Sprintf("<pre>%s</pre>", m.formatOutput(stderr))
	}

	elapsed := time.Since(startTime).Seconds()
	cmdEsc := html.EscapeString(cmdText)

	// slot_label: "| <code>@N</code>" when slot != "1", else ""
	// Matches Python: slot_label = f"| <code>@{html.escape(slot)}</code>" if slot != "1" else ""
	slotLabel := ""
	if slot != "1" {
		slotLabel = fmt.Sprintf("| <code>@%s</code>", html.EscapeString(slot))
	}

	// Shell name (TODO: read from module config; default: bash)
	shellName := "bash"

	if final || done {
		emoji := termEmojiDone
		if exitCode != 0 {
			emoji = termEmojiDoneError
		}

		// extra: "{📰} <b>exit code:</b> <mono>N</mono>\n"
		extra := fmt.Sprintf("%s <b>%s</b> <mono>%d</mono>\n",
			termEmojiNewspaper, s(m.k, "terminal", "exit_code"), exitCode)

		// footer: "<blockquote>{🉐} <b>Shell:</b> bash | {🧮} <b>completed in</b> <mono>1.23 sec.</mono></blockquote>"
		footerParts := []string{
			fmt.Sprintf("%s <b>%s</b> %s", termEmojiGoodLuck, s(m.k, "terminal", "shell"), html.EscapeString(shellName)),
		}
		footerParts = append(footerParts, fmt.Sprintf("%s <b>%s</b> <mono>%.2f %s</mono>",
			termEmojiAbacus, s(m.k, "terminal", "completed_in"), elapsed, s(m.k, "terminal", "seconds")))
		footer := fmt.Sprintf("<blockquote>%s</blockquote>", strings.Join(footerParts, " | "))

		// Python format:
		// f"{emoji} <b>{system_command}</b> {slot_label} <blockquote><code>{cmd}</code></blockquote>\n{extra}{stdout}{stderr}{footer}"
		header := fmt.Sprintf("%s <b>%s</b> %s <blockquote><code>%s</code></blockquote>\n",
			emoji, s(m.k, "terminal", "system_command"), slotLabel, cmdEsc)
		return header + extra + stdoutBlock + stderrBlock + footer
	}

	// Running state
	footerParts := []string{
		fmt.Sprintf("%s <b>%s</b> %s", termEmojiGoodLuck, s(m.k, "terminal", "shell"), html.EscapeString(shellName)),
	}
	footerParts = append(footerParts, fmt.Sprintf("%s <b>%s</b> <mono>%.2f %s</mono>",
		termEmojiAbacus, s(m.k, "terminal", "running_time"), elapsed, s(m.k, "terminal", "seconds")))
	footer := fmt.Sprintf("<blockquote>%s</blockquote>", strings.Join(footerParts, " | "))

	// Python format (running):
	// f"{loading} <i>{system_command}</i> {slot_label} <blockquote><code>{cmd}</code></blockquote>\n{stdout}{stderr}{footer}"
	header := fmt.Sprintf("%s <i>%s</i> %s <blockquote><code>%s</code></blockquote>\n",
		termEmojiLoading, s(m.k, "terminal", "system_command"), slotLabel, cmdEsc)
	return header + stdoutBlock + stderrBlock + footer
}

// startStreaming launches goroutines to read stdout/stderr and update the message periodically.
func (m *terminalModule) startStreaming(bgCtx context.Context, rc *runningCmd) {
	k := m.k
	key := m.slotKey(rc.chatID, rc.slot)

	var wg sync.WaitGroup

	// Read stdout goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		if rc.stdoutPipe == nil {
			return
		}
		scanner := bufio.NewScanner(rc.stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			rc.mu.Lock()
			rc.stdout.WriteString(line + "\n")
			rc.mu.Unlock()
		}
	}()

	// Read stderr goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		if rc.stderrPipe == nil {
			return
		}
		scanner := bufio.NewScanner(rc.stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			rc.mu.Lock()
			rc.stderr.WriteString(line + "\n")
			rc.mu.Unlock()
		}
	}()

	// Periodic update ticker (every update_interval seconds; default 3)
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rc.mu.Lock()
				done := rc.done
				rc.mu.Unlock()
				if done {
					return
				}
				msg := m.buildMessage(rc, false)
				_ = editHTML(bgCtx, k, rc.chatID, rc.msgID, msg)
			case <-bgCtx.Done():
				return
			}
		}
	}()

	// Wait for streams to close, then send final update
	go func() {
		wg.Wait()
		_ = rc.cmd.Wait()

		rc.mu.Lock()
		rc.done = true
		if rc.cmd.ProcessState != nil {
			rc.exitCode = rc.cmd.ProcessState.ExitCode()
		}
		rc.mu.Unlock()

		msg := m.buildMessage(rc, true)
		_ = editHTML(bgCtx, k, rc.chatID, rc.msgID, msg)

		m.mu.Lock()
		delete(m.running, key)
		m.mu.Unlock()
	}()
}

// ---------- .t ----------

func (m *terminalModule) cmdT(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i>", termEmojiSpeech, s(m.k, "terminal", "command_not_specified")))
	}

	slot, cmdStr := parseSlot(parts[1])

	// Handle -q flag (quiet mode for piped use)
	_ = strings.HasPrefix(cmdStr, "-q ") // TODO: implement piped mode

	if strings.TrimSpace(cmdStr) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i>", termEmojiSpeech, s(m.k, "terminal", "command_not_specified")))
	}

	key := m.slotKey(ev.PeerID, slot)

	m.mu.Lock()
	if _, exists := m.running[key]; exists {
		m.mu.Unlock()
		// Python: f"{CUSTOM_EMOJI['🗯']} <i>{lang['command_already_running']}{slot_label}</i>"
		// slot_label here is " (@N)" plain text (different from buildMessage's slot_label)
		slotSuffix := ""
		if slot != "1" {
			slotSuffix = " (@" + slot + ")"
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s%s</i>", termEmojiSpeech, s(m.k, "terminal", "command_already_running"), slotSuffix))
	}

	cmd := exec.Command("bash", "-c", cmdStr)
	// Create new session/process group so we can kill the whole group (matches Python's use_setsid=True)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i> <code>%s</code>",
				termEmojiSpeech, s(m.k, "terminal", "launch_error"), html.EscapeString(err.Error())))
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i> <code>%s</code>",
				termEmojiSpeech, s(m.k, "terminal", "launch_error"), html.EscapeString(err.Error())))
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i> <code>%s</code>",
				termEmojiSpeech, s(m.k, "terminal", "launch_error"), html.EscapeString(err.Error())))
	}

	rc := &runningCmd{
		cmd:        cmd,
		stdoutPipe: stdoutPipe,
		stderrPipe: stderrPipe,
		msgID:      ev.Raw.ID,
		chatID:     ev.PeerID,
		slot:       slot,
		startTime:  time.Now(),
		stdin:      stdinPipe,
		command:    cmdStr,
	}

	m.running[key] = rc
	m.mu.Unlock()

	// Initial "executing" message – matches Python's initial message format exactly:
	// f"{loading} <i>{system_command}</i>{slot_label} <blockquote><code>{cmd}</code></blockquote>\n{❄️} <i>{executing}</i>"
	// Note: initial slot_label uses " <code>@N</code>" (with leading space, no "|")
	initSlotLabel := ""
	if slot != "1" {
		initSlotLabel = fmt.Sprintf(" <code>@%s</code>", html.EscapeString(slot))
	}
	initMsg := fmt.Sprintf("%s <i>%s</i>%s <blockquote><code>%s</code></blockquote>\n%s <i>%s</i>",
		termEmojiLoading, s(m.k, "terminal", "system_command"), initSlotLabel,
		html.EscapeString(cmdStr), termEmojiSnowflake, s(m.k, "terminal", "executing"))
	_ = editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, initMsg)

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		delete(m.running, key)
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i> <code>%s</code>",
				termEmojiSpeech, s(m.k, "terminal", "launch_error"), html.EscapeString(err.Error())))
	}

	// Store PID after Start()
	rc.mu.Lock()
	rc.pid = cmd.Process.Pid
	rc.mu.Unlock()

	// Run streaming in background goroutines; pass detached context so it
	// survives the HTTP handler returning.
	m.startStreaming(context.Background(), rc)
	return nil
}

// ---------- .tkill ----------

func (m *terminalModule) cmdTkill(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	slot := "1"
	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		s2, _ := parseSlot(strings.TrimSpace(parts[1]))
		slot = s2
	}

	if slot == "all" {
		m.mu.Lock()
		var toKill []*runningCmd
		for _, rc := range m.running {
			if rc.chatID == ev.PeerID {
				toKill = append(toKill, rc)
			}
		}
		m.mu.Unlock()

		if len(toKill) == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("%s <i>%s</i>", termEmojiSpeech, s(m.k, "terminal", "no_running_commands")))
		}
		for _, rc := range toKill {
			m.killProcess(rc)
		}
		count := len(toKill)
		// Python: f"{CUSTOM_EMOJI['☑️']} <i>{lang['command_stopped']} ({count})</i>"
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s (%d)</i>", termEmojiCheckBox, s(m.k, "terminal", "command_stopped"), count))
	}

	key := m.slotKey(ev.PeerID, slot)
	m.mu.Lock()
	rc := m.running[key]
	m.mu.Unlock()

	if rc == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i>", termEmojiSpeech, s(m.k, "terminal", "no_running_commands")))
	}

	rc.mu.Lock()
	done := rc.done
	rc.mu.Unlock()
	if done {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i>", termEmojiInfoBubble, s(m.k, "terminal", "already_completed")))
	}

	if err := m.killProcess(rc); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i> <pre>%s</pre>",
				termEmojiSpeech, s(m.k, "terminal", "stop_error"), html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s <i>%s</i>", termEmojiCheckBox, s(m.k, "terminal", "command_stopped")))
}

// killProcess sends SIGTERM then SIGKILL to the process group (matches Python's _kill_one logic).
func (m *terminalModule) killProcess(rc *runningCmd) error {
	if rc.cmd == nil || rc.cmd.Process == nil {
		return nil
	}
	pid := rc.cmd.Process.Pid
	// Send SIGTERM to process group (-pid)
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		// If SIGTERM fails, fall back to direct SIGKILL on process
		_ = rc.cmd.Process.Kill()
		return nil
	}
	time.Sleep(1 * time.Second)
	// Check if still running (ProcessState is set after Wait completes)
	rc.mu.Lock()
	done := rc.done
	rc.mu.Unlock()
	if !done {
		// Send SIGKILL to process group
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
	return nil
}

// ---------- .ti ----------

func (m *terminalModule) cmdTi(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	text := ev.Text()
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 || strings.TrimSpace(parts[1]) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i>", termEmojiSpeech, s(m.k, "terminal", "command_not_specified")))
	}

	slot, inputText := parseSlot(parts[1])
	if strings.TrimSpace(inputText) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s</i>", termEmojiSpeech, s(m.k, "terminal", "command_not_specified")))
	}

	key := m.slotKey(ev.PeerID, slot)
	m.mu.Lock()
	rc := m.running[key]
	m.mu.Unlock()

	// slot_label: " <code>@N</code>" when slot != "1" (matches Python's ti slot_label)
	slotLabel := ""
	if slot != "1" {
		slotLabel = fmt.Sprintf(" <code>@%s</code>", html.EscapeString(slot))
	}

	if rc == nil {
		// Python: f"{CUSTOM_EMOJI['🗯']} <i>{lang['no_running_commands']}{slot_label}</i>"
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s%s</i>", termEmojiSpeech, s(m.k, "terminal", "no_running_commands"), slotLabel))
	}

	rc.mu.Lock()
	done := rc.done
	rc.mu.Unlock()
	if done {
		// Python: f"{CUSTOM_EMOJI['💬']} <i>{lang['already_completed']}{slot_label}</i>"
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>%s%s</i>", termEmojiInfoBubble, s(m.k, "terminal", "already_completed"), slotLabel))
	}
	if rc.stdin == nil {
		// Python: f"{CUSTOM_EMOJI['⚠️']} <i>stdin{slot_label} closed (stdin_eof enabled?)</i>"
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>stdin%s closed (stdin_eof enabled?)</i>", termEmojiWarning, slotLabel))
	}

	_, err := fmt.Fprintln(rc.stdin, inputText)
	if err != nil {
		// Python: f"{CUSTOM_EMOJI['🗯']} <i>stdin error{slot_label}:</i> <code>{err}</code>"
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>stdin error%s:</i> <code>%s</code>",
				termEmojiSpeech, slotLabel, html.EscapeString(err.Error())))
	}

	// Python: f"{CUSTOM_EMOJI['☑️']} <i>stdin{slot_label} ← </i><code>{text}</code>"
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s <i>stdin%s ← </i><code>%s</code>",
			termEmojiCheckBox, slotLabel, html.EscapeString(inputText)))
}
