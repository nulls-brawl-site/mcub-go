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
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// ansiRe matches ANSI terminal escape sequences.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[mABCDEFGHJKSTfhilmnprsu]`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// parseSlot extracts an optional "@N" slot prefix from text.
// "@2 ls -la" → ("2", "ls -la"); "ls -la" → ("1", "ls -la").
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
		{Name: "t", Description: "[@N] <command> execute shell command", Handler: m.cmdT},
		{Name: "tkill", Description: "[@N|@all] kill running terminal command", Handler: m.cmdTkill},
		{Name: "ti", Description: "[@N] <text> send text to stdin of running command", Handler: m.cmdTi},
	}
}

// ---------- constants ----------

const (
	termEmojiLoading = `🔘`
	termEmojiDone    = `☑️`
	termEmojiError   = `😖`
	termEmojiWarn    = `🗯`
)

// ---------- helpers ----------

func (m *terminalModule) slotKey(chatID int64, slot string) string {
	return fmt.Sprintf("%d:%s", chatID, slot)
}

// buildMessage formats the running/final message.
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

	stdout = stripANSI(stdout)
	stderr = stripANSI(stderr)

	// Truncate long output (show tail 2000 chars)
	const maxOut = 2000
	if len(stdout) > maxOut {
		stdout = "...\n" + stdout[len(stdout)-maxOut:]
	}

	elapsed := time.Since(startTime).Seconds()
	cmdEsc := html.EscapeString(cmdText)
	slotLabel := ""
	if slot != "1" {
		slotLabel = fmt.Sprintf(" | <code>@%s</code>", html.EscapeString(slot))
	}

	stdoutBlock := fmt.Sprintf("<pre>%s</pre>", html.EscapeString(stdout))
	stderrBlock := ""
	if strings.TrimSpace(stderr) != "" {
		stderrBlock = fmt.Sprintf("<pre>%s</pre>", html.EscapeString(stderr))
	}

	if final || done {
		emoji := termEmojiDone
		if exitCode != 0 {
			emoji = termEmojiError
		}
		footer := fmt.Sprintf(
			"<blockquote>📰 <b>Exit code</b> %d\n⚗️ Shell: bash | Completed in: %.2fs</blockquote>",
			exitCode, elapsed,
		)
		return fmt.Sprintf(
			"%s <b>Command</b>%s\n<blockquote><code>%s</code></blockquote>\n\n%s%s%s",
			emoji, slotLabel, cmdEsc, stdoutBlock, stderrBlock, footer,
		)
	}

	footer := fmt.Sprintf(
		"<blockquote>Shell: bash | Running: %.2fs</blockquote>",
		elapsed,
	)
	return fmt.Sprintf(
		"%s <i>Command</i>%s\n<blockquote><code>%s</code></blockquote>\n\n%s%s%s",
		termEmojiLoading, slotLabel, cmdEsc, stdoutBlock, stderrBlock, footer,
	)
}

// startStreaming launches goroutines to read stdout/stderr and update the message.
// Pipes must already be created on rc before calling this.
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

	// Periodic update ticker (every 3 seconds)
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

	// Wait for completion and send final update
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
			termEmojiWarn+` <i>No command specified. Usage: .t [@slot] &lt;command&gt;</i>`)
	}

	slot, cmdStr := parseSlot(parts[1])
	if strings.TrimSpace(cmdStr) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			termEmojiWarn+` <i>No command specified.</i>`)
	}

	key := m.slotKey(ev.PeerID, slot)

	m.mu.Lock()
	if _, exists := m.running[key]; exists {
		m.mu.Unlock()
		slotSuffix := ""
		if slot != "1" {
			slotSuffix = " (@" + slot + ")"
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>Command already running%s</i>", termEmojiWarn, slotSuffix))
	}

	cmd := exec.Command("bash", "-c", cmdStr)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>stdout pipe error: %s</i>", termEmojiWarn, html.EscapeString(err.Error())))
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>stderr pipe error: %s</i>", termEmojiWarn, html.EscapeString(err.Error())))
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>stdin pipe error: %s</i>", termEmojiWarn, html.EscapeString(err.Error())))
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

	// Show initial "running" message
	slotLabel := ""
	if slot != "1" {
		slotLabel = fmt.Sprintf(" | <code>@%s</code>", html.EscapeString(slot))
	}
	initMsg := fmt.Sprintf("%s <i>Command</i>%s\n<blockquote><code>%s</code></blockquote>\n\n❄️ <i>Executing...</i>",
		termEmojiLoading, slotLabel, html.EscapeString(cmdStr))
	_ = editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, initMsg)

	if err := cmd.Start(); err != nil {
		m.mu.Lock()
		delete(m.running, key)
		m.mu.Unlock()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>Failed to start command: %s</i>", termEmojiWarn, html.EscapeString(err.Error())))
	}

	// Run streaming in background goroutines; pass a detached context so it
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
		s, _ := parseSlot(strings.TrimSpace(parts[1]))
		slot = s
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
				termEmojiWarn+` <i>No running commands</i>`)
		}
		for _, rc := range toKill {
			if rc.cmd != nil && rc.cmd.Process != nil {
				_ = rc.cmd.Process.Kill()
			}
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>Stopped %d command(s)</i>", termEmojiDone, len(toKill)))
	}

	key := m.slotKey(ev.PeerID, slot)
	m.mu.Lock()
	rc := m.running[key]
	m.mu.Unlock()

	if rc == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			termEmojiWarn+` <i>No running command in this slot</i>`)
	}

	rc.mu.Lock()
	done := rc.done
	rc.mu.Unlock()
	if done {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			`💬 <i>Command already completed</i>`)
	}

	if rc.cmd != nil && rc.cmd.Process != nil {
		_ = rc.cmd.Process.Kill()
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		termEmojiDone+` <i>Command stopped</i>`)
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
			termEmojiWarn+` <i>Usage: .ti [@slot] &lt;text&gt;</i>`)
	}

	slot, inputText := parseSlot(parts[1])
	if strings.TrimSpace(inputText) == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			termEmojiWarn+` <i>No text specified</i>`)
	}

	key := m.slotKey(ev.PeerID, slot)
	m.mu.Lock()
	rc := m.running[key]
	m.mu.Unlock()

	slotLabel := ""
	if slot != "1" {
		slotLabel = fmt.Sprintf(" <code>@%s</code>", html.EscapeString(slot))
	}

	if rc == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>No running command%s</i>", termEmojiWarn, slotLabel))
	}

	rc.mu.Lock()
	done := rc.done
	rc.mu.Unlock()
	if done {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf(`💬 <i>Command already completed%s</i>`, slotLabel))
	}
	if rc.stdin == nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf(`⚠️ <i>stdin%s closed</i>`, slotLabel))
	}

	_, err := fmt.Fprintln(rc.stdin, inputText)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <i>stdin error%s:</i> <code>%s</code>",
				termEmojiWarn, slotLabel, html.EscapeString(err.Error())))
	}

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s <i>stdin%s ← </i><code>%s</code>",
			termEmojiDone, slotLabel, html.EscapeString(inputText)))
}
