package modules

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubtypes "github.com/nulls-brawl-site/telegram-mcub-go/types"

	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji IDs used by the tester module (exact matches from Python source).
const (
	emojiPen       = `<tg-emoji emoji-id="5334673106202010226">✏️</tg-emoji>`
	emojiSnowflake = `<tg-emoji emoji-id="5431895003821513760">❄️</tg-emoji>`
	emojiSpeech    = `<tg-emoji emoji-id="5465132703458270101">🗯</tg-emoji>`
	emojiIce       = `<tg-emoji emoji-id="5404728536810398694">🧊</tg-emoji>`
	emojiCheck     = `<tg-emoji emoji-id="5454096630372379732">☑️</tg-emoji>`
	emojiFile      = `<tg-emoji emoji-id="5433653135799228968">📁</tg-emoji>`
	emojiNote      = `<tg-emoji emoji-id="5334882760735598374">📝</tg-emoji>`
	emojiPrinter   = `<tg-emoji emoji-id="5386494631112353009">🖨</tg-emoji>`
	emojiNewspaper = `<tg-emoji emoji-id="5433982607035474385">📰</tg-emoji>`
	emojiBallotBox = `<tg-emoji emoji-id="5359741159566484212">🗳</tg-emoji>`
	emojiSatellite = `<tg-emoji emoji-id="5321304062715517873">🛰</tg-emoji>`
)

// testerModule implements the "tester" system module.
type testerModule struct {
	k *kernel.Kernel
}

func newTesterModule() loader.Module { return &testerModule{} }

// Name implements loader.Module.
func (m *testerModule) Name() string { return "tester" }

// OnLoad implements loader.Module.
func (m *testerModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("tester: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *testerModule) OnUnload(k interface{}) error {
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
func (m *testerModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "ping", Description: "check bot latency", Handler: m.cmdPing},
		{Name: "logs", Description: "show/clear kernel logs", Handler: m.cmdLogs},
		{Name: "freezing", Description: "freeze userbot for N seconds", Handler: m.cmdFreezing},
		{Name: "teaser", Description: "test a command with logging", Handler: m.cmdTeaser},
	}
}

// ---------- helpers ----------

func (m *testerModule) logsDir() string {
	return "logs"
}

func (m *testerModule) kernelLogPath() string {
	return filepath.Join(m.logsDir(), "kernel.log")
}

func (m *testerModule) detectBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return strings.TrimSpace(string(out))
	}
	return "main"
}

// formatUptime formats a duration using langpack time-unit strings.
// Output matches Python: "1h 2m 3s", "2m 3s", or "3s".
func (m *testerModule) formatUptime(d time.Duration) string {
	total := int(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60

	h := s(m.k, "tester", "hours")
	mi := s(m.k, "tester", "minutes")
	sec := s(m.k, "tester", "seconds")

	if hours > 0 {
		return fmt.Sprintf("%d%s %d%s %d%s", hours, h, minutes, mi, seconds, sec)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d%s %d%s", minutes, mi, seconds, sec)
	}
	return fmt.Sprintf("%d%s", seconds, sec)
}

// ---------- .ping ----------

// cmdPing edits the message, measures latency, and reports ping + uptime.
// Output matches Python tester.py cmd_ping exactly.
func (m *testerModule) cmdPing(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}

	defer func() {
		if r := recover(); r != nil {
			// On any panic, try to show error_logs message
			_ = editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				sf(m.k, "tester", "error_logs", map[string]interface{}{"snowflake": emojiSnowflake}))
		}
	}()

	start := time.Now()
	// First edit: use emojiPen (matching Python's _resolve_ping_start_emoji() default = ✏️)
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, emojiPen); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "tester", "error_logs", map[string]interface{}{"snowflake": emojiSnowflake}))
	}
	pingMs := float64(time.Since(start).Microseconds()) / 1000.0

	uptime := m.formatUptime(m.k.Uptime())

	pingLabel := s(m.k, "tester", "ping")
	msLabel := s(m.k, "tester", "ms")
	uptimeLabel := s(m.k, "tester", "uptime")

	// Python format:
	// f"""<blockquote>{start_emoji} <b>{strings("ping")}:</b> {ping_time} {strings("ms")}</blockquote>
	// <blockquote>{start_emoji} <b>{strings("uptime")}:</b> {uptime}</blockquote>"""
	resp := fmt.Sprintf(
		`<blockquote>%s <b>%s:</b> %.2f %s</blockquote>`+"\n"+
			`<blockquote>%s <b>%s:</b> %s</blockquote>`,
		emojiPen, pingLabel, pingMs, msLabel,
		emojiPen, uptimeLabel, uptime,
	)
	// Banner: banner_url default = .../img/ping.png (matches Python tester.py on_load config)
	branch := m.detectBranch()
	bannerURL := fmt.Sprintf(
		"https://raw.githubusercontent.com/nulls-brawl-site/mcub-go/refs/heads/%s/img/ping.png",
		branch,
	)
	if err := editHTMLWithBanner(ctx, m.k, ev.PeerID, ev.Raw.ID, resp, bannerURL, false); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, resp)
	}
	return nil
}

// ---------- .logs ----------

// logLevels are the valid log level names.
var logLevels = map[string]bool{
	"debug": true, "info": true, "warning": true,
	"error": true, "critical": true, "all": true,
}

// cmdLogs sends or filters kernel.log, or shows an inline level selector.
func (m *testerModule) cmdLogs(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	logPath := m.kernelLogPath()

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "tester", "logs_not_found", map[string]interface{}{"file": emojiFile}))
	}

	info, _ := os.Stat(logPath)
	if info != nil && info.Size() == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s %s", emojiBallotBox, s(m.k, "tester", "file_empty")))
	}

	// Parse args: strip prefix + "logs"
	args := m.parseArgs(ev)

	if len(args) == 0 {
		// Show level selector with inline buttons.
		text := sf(m.k, "tester", "logs_choose_level", map[string]interface{}{"paper": emojiNewspaper}) +
			"\n" + s(m.k, "tester", "logs_choose_desc")
		buttons := mcubtypes.ButtonGrid{
			mcubtypes.ButtonRow{newButton("DEBUG", "logs:level:debug"), newButton("INFO", "logs:level:info")},
			mcubtypes.ButtonRow{newButton("WARNING", "logs:level:warning"), newButton("ERROR", "logs:level:error")},
			mcubtypes.ButtonRow{newButton("CRITICAL", "logs:level:critical"), newButton("ALL", "logs:level:all")},
			mcubtypes.ButtonRow{newButton("✖", "logs:cancel")},
		}
		return sendHTMLWithButtons(ctx, m.k, ev.PeerID, text, buttons)
	}

	arg0 := strings.ToLower(args[0])

	if arg0 == "clear" {
		if err := os.WriteFile(logPath, nil, 0o644); err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("%s Error clearing logs: %v", emojiSnowflake, err))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s %s", emojiBallotBox, s(m.k, "tester", "logs_clear")))
	}

	if arg0 == "tail" {
		n := 20
		if len(args) > 1 {
			_, err := fmt.Sscanf(args[1], "%d", &n)
			if err != nil {
				n = 20
			}
		}
		lines, err := tailLines(logPath, n)
		if err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("%s Error reading logs: %v", emojiSnowflake, err))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("<pre>%s</pre>", strings.Join(lines, "\n")))
	}

	if !logLevels[arg0] {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s %s", emojiIce, s(m.k, "tester", "logs_not_fount_args")))
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		sf(m.k, "tester", "logs_sending", map[string]interface{}{"printer": emojiPrinter})); err != nil {
		return err
	}
	return m.sendLogsLevel(ctx, ev.PeerID, arg0, logPath)
}

// sendLogsLevel sends the filtered kernel.log to the given peer.
// Caption uses the logs_level_caption langpack template to match Python exactly.
func (m *testerModule) sendLogsLevel(ctx context.Context, peerID int64, level, logPath string) error {
	targetPath := logPath
	var tempPath string

	if level != "all" {
		// Filter log by level.
		filtered, err := filterLogByLevel(logPath, strings.ToUpper(level))
		if err != nil || filtered == "" {
			return sendHTML(ctx, m.k, peerID,
				fmt.Sprintf("%s %s", emojiBallotBox, s(m.k, "tester", "file_empty")))
		}
		tempPath = filtered
		targetPath = filtered
	}

	defer func() {
		if tempPath != "" {
			os.Remove(tempPath)
		}
	}()

	branch := m.detectBranch()

	// Use logs_level_caption langpack template (matches Python _send_logs exactly).
	// Python passes: logs_title, logs, mcub, pen, kernel_version, version,
	//                commit_url, commit_sha, satellite, branch_label, branch, printer, level
	caption := sf(m.k, "tester", "logs_level_caption", map[string]interface{}{
		"logs_title":     emojiNote,
		"logs":           s(m.k, "tester", "logs"),
		"mcub":           "MCUB",
		"pen":            emojiPen,
		"kernel_version": s(m.k, "tester", "kernel_version"),
		"version":        m.k.Version,
		"commit_url":     "#",
		"commit_sha":     m.k.Version,
		"satellite":      emojiSatellite,
		"branch_label":   s(m.k, "tester", "branch"),
		"branch":         branch,
		"printer":        emojiPrinter,
		"level":          strings.ToUpper(level),
	})

	return sendDocument(ctx, m.k, peerID, targetPath, caption)
}

// filterLogByLevel creates a temp file with only log lines at the given level.
// Returns the temp file path or empty string if nothing was written.
func filterLogByLevel(logPath, levelUpper string) (string, error) {
	src, err := os.Open(logPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	tmp, err := os.CreateTemp("", "kernel.*.log")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	wrote := false
	keepBlock := false
	scanner := bufio.NewScanner(src)
	for scanner.Scan() {
		line := scanner.Text()
		// Check if this line starts a new log entry.
		if len(line) > 10 && strings.Contains(line, " [") {
			if strings.Contains(line, " ["+levelUpper+"] ") {
				keepBlock = true
			} else if isLogLevelMarker(line) {
				keepBlock = false
			}
		}
		if keepBlock {
			fmt.Fprintln(tmp, line)
			wrote = true
		}
	}
	tmp.Close()

	if !wrote {
		os.Remove(tmpPath)
		return "", nil
	}
	return tmpPath, nil
}

// isLogLevelMarker checks whether a line contains a known log-level marker.
func isLogLevelMarker(line string) bool {
	for _, lvl := range []string{"DEBUG", "INFO", "WARN", "WARNING", "ERROR", "CRITICAL"} {
		if strings.Contains(line, " ["+lvl+"] ") {
			return true
		}
	}
	return false
}

// tailLines returns the last n lines of a file.
func tailLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, sc.Err()
}

// parseArgs strips the prefix+command and returns remaining tokens.
func (m *testerModule) parseArgs(ev *events.NewMessage) []string {
	body := ev.Text()
	if m.k != nil {
		body = strings.TrimPrefix(body, m.k.Prefix())
	}
	parts := strings.Fields(body)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// ---------- .freezing ----------

// cmdFreezing simulates a userbot freeze by sleeping for N seconds.
// Strings match Python tester.py cmd_freezing exactly (via langpacks).
// Note: Python additionally disconnects/reconnects the Telegram client; Go only sleeps.
func (m *testerModule) cmdFreezing(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	args := m.parseArgs(ev)
	prefix := "."
	if m.k != nil {
		prefix = m.k.Prefix()
	}

	if len(args) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "tester", "freezing_usage", map[string]interface{}{
				"speech": emojiSpeech,
				"prefix": prefix,
			}))
	}

	var seconds int
	if _, err := fmt.Sscanf(args[0], "%d", &seconds); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "tester", "freezing_number", map[string]interface{}{"speech": emojiSpeech}))
	}
	if seconds <= 0 || seconds > 60 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "tester", "freezing_range", map[string]interface{}{"speech": emojiSpeech}))
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		sf(m.k, "tester", "freezing_start", map[string]interface{}{
			"snowflake": emojiIce,
			"seconds":   seconds,
		})); err != nil {
		return err
	}

	time.Sleep(time.Duration(seconds) * time.Second)

	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		sf(m.k, "tester", "freezing_done", map[string]interface{}{
			"check":   emojiCheck,
			"seconds": seconds,
		}))
}

// ---------- .teaser ----------

// cmdTeaser executes a command with full kernel-log recording.
// Sends a temp HTML report file matching Python tester.py cmd_teaser exactly.
func (m *testerModule) cmdTeaser(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	prefix := "."
	if m.k != nil {
		prefix = m.k.Prefix()
	}

	args := m.parseArgs(ev)
	if len(args) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "tester", "teaser_no_cmd", map[string]interface{}{"prefix": prefix}))
	}

	cmdName := args[0]
	rawText := strings.Join(args, " ")

	handler, exists := m.k.CommandHandlers[cmdName]
	if !exists {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "tester", "teaser_cmd_not_found", map[string]interface{}{"cmd": cmdName}))
	}

	logPath := m.kernelLogPath()
	var initialSize int64
	if info, err := os.Stat(logPath); err == nil {
		initialSize = info.Size()
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		sf(m.k, "tester", "teaser_recording", map[string]interface{}{"cmd": rawText})); err != nil {
		return err
	}

	// Build a fake event with text = prefix + rawText (e.g. ".reload terminal")
	// so the sub-command handler receives the correct command text.
	var fakeEv *events.NewMessage
	if ev.Raw != nil {
		rawCopy := *ev.Raw
		rawCopy.Message = prefix + rawText
		fakeEv = &events.NewMessage{}
		*fakeEv = *ev
		fakeEv.Raw = &rawCopy
	} else {
		fakeEv = ev
	}

	// Execute the handler (edits will go to the same message).
	_ = handler(ctx, fakeEv)

	// Gather any new kernel log entries since before handler ran.
	newEntries := ""
	if f, err := os.Open(logPath); err == nil {
		if _, err2 := f.Seek(initialSize, 0); err2 == nil {
			sc := bufio.NewScanner(f)
			var sb strings.Builder
			for sc.Scan() {
				sb.WriteString(sc.Text())
				sb.WriteByte('\n')
			}
			newEntries = strings.TrimSpace(sb.String())
		}
		f.Close()
	}

	// Build report matching Python's report_parts structure.
	// Python: header + event_log (or "No event calls") + kernel_log (or empty)
	report := sf(m.k, "tester", "teaser_report_header", map[string]interface{}{"cmd": rawText})
	// Note: Go doesn't have _FakeEventProxy so there's no event call log
	report += "<b>No event calls recorded</b>\n"
	if newEntries != "" {
		report += sf(m.k, "tester", "teaser_kernel_log", map[string]interface{}{"log": newEntries})
	} else {
		report += s(m.k, "tester", "teaser_empty_log")
	}

	// Write report to temp HTML file and send it (matches Python exactly).
	tmp, err := os.CreateTemp("", "teaser_*.html")
	if err == nil {
		defer os.Remove(tmp.Name())
		_, _ = tmp.WriteString("<html><body><pre>" + report + "</pre></body></html>")
		_ = tmp.Close()

		caption := sf(m.k, "tester", "teaser_done", map[string]interface{}{"cmd": rawText})
		if sendErr := sendDocument(ctx, m.k, ev.PeerID, tmp.Name(), caption); sendErr != nil {
			// Fallback: edit with caption only
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, caption)
		}
		return nil
	}

	// Fallback if temp file creation fails
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		sf(m.k, "tester", "teaser_done", map[string]interface{}{"cmd": rawText}))
}
