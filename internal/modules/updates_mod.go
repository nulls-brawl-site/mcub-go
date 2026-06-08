package modules

import (
	"context"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji IDs for the updates module (exact from Python source).
const (
	emojiTelescope = `<tg-emoji emoji-id="5310041868191407556">🔭</tg-emoji>`
	emojiAlembic   = `<tg-emoji emoji-id="5332654441508119011">⚗️</tg-emoji>`
	emojiMagnet    = `<tg-emoji emoji-id="5372892693024218813">🧲</tg-emoji>`
	emojiError     = `<tg-emoji emoji-id="5388785832956016892">❌</tg-emoji>`
	emojiOKGreen   = `<tg-emoji emoji-id="5902002809573740949">✅</tg-emoji>`
)

// kaomojis is the exact list from Python updates.py.
var kaomojis = []string{
	"ಠ_ಠ", "( ཀ ʖ̯ ཀ)", "(◕‿◕✿)", "(つ･･)つ", "༼つ◕_◕༽つ",
	"(•_•)", "☜(ﾟヮﾟ☜)", "(☞ﾟヮﾟ)☞", "ʕ•ᴥ•ʔ", "(づ￣ ³￣)づ",
	">_<", "0_o",
}

func randomFace() string {
	return kaomojis[rand.Intn(len(kaomojis))]
}

// updatesModule implements the "updates" system module.
type updatesModule struct {
	k *kernel.Kernel
}

func newUpdatesModule() loader.Module { return &updatesModule{} }

// Name implements loader.Module.
func (m *updatesModule) Name() string { return "updates" }

// OnLoad implements loader.Module.
func (m *updatesModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("updates: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *updatesModule) OnUnload(k interface{}) error {
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
func (m *updatesModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "restart", Description: "restart userbot", Handler: m.cmdRestart},
		{Name: "update", Description: "update MCUB from git and restart", Handler: m.cmdUpdate},
		{Name: "stop", Description: "stop userbot", Handler: m.cmdStop},
	}
}

// ---------- helpers ----------

func (m *updatesModule) detectBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		if branch := strings.TrimSpace(string(out)); branch != "" {
			return branch
		}
	}
	return "main"
}

// mcubName returns the MCUB display name.
// Non-premium fallback matches Python: "MCUB".
func (m *updatesModule) mcubName() string {
	return "MCUB"
}

// ---------- .restart ----------

// cmdRestart sends a restarting message and re-execs the process.
// Message format matches Python:
//
//	<blockquote>{telescope} <i>Your <b>MCUB</b> is restarting...</i></blockquote>
func (m *updatesModule) cmdRestart(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	// Python: f"<blockquote>{PREMIUM_EMOJI['telescope']} <i>{_s('restarting').format(mcub=mcub_handler())}</i></blockquote>"
	// langpack restarting: 'Your <b>{mcub}</b> is restarting...'
	restartingText := sf(m.k, "updates", "restarting", map[string]interface{}{"mcub": m.mcubName()})
	msg := fmt.Sprintf(`<blockquote>%s <i>%s</i></blockquote>`, emojiTelescope, restartingText)

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, msg); err != nil {
		return err
	}

	return m.k.Restart()
}

// ---------- .update ----------

// cmdUpdate pulls the latest git changes and restarts the process.
// Flow matches Python updates.py cmd_update exactly.
func (m *updatesModule) cmdUpdate(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	// Show initial indicator.
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, "❄️"); err != nil {
		return err
	}

	branch := m.detectBranch()

	// Python uses subprocess.run(capture_output=True) which captures stdout only on success.
	// Use CombinedOutput so we get stderr too for error messages, but for "Already up to date"
	// and success output, Python uses result.stdout only.
	gitCmd := exec.Command("git", "pull", "origin", branch)
	var gitStdout, gitStderr strings.Builder
	gitCmd.Stdout = &gitStdout
	gitCmd.Stderr = &gitStderr
	err := gitCmd.Run()
	if err != nil {
		// Python: subprocess.run with returncode check is inside try/except for OS errors.
		// If git pull returns non-zero, Python falls through without displaying anything.
		// Go: show error message from stderr for better UX.
		errText := gitStderr.String()
		if errText == "" {
			errText = err.Error()
		}
		errMsg := sf(m.k, "updates", "error", map[string]interface{}{"error": errText})
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, errMsg)
	}

	// Use stdout for output display (matches Python result.stdout)
	output := gitStdout.String()

	if strings.Contains(output, "Already up to date") {
		// langpack already_updated: '<tg-emoji ...>✅</tg-emoji> <b>Already latest version {version}</b>'
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "updates", "already_updated", map[string]interface{}{"version": m.k.Version}))
	}

	// Successful pull.
	outPreview := output
	if len(outPreview) > 200 {
		outPreview = outPreview[:200]
	}
	// langpack git_pull_success: '<tg-emoji ...>📝</tg-emoji> <b>Git pull successful!</b>\n\n<code>{output}</code>'
	pullMsg := sf(m.k, "updates", "git_pull_success", map[string]interface{}{"output": outPreview})
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, pullMsg); err != nil {
		return err
	}

	time.Sleep(2 * time.Second)

	face := randomFace()
	// langpack update_success: '<tg-emoji ...>⚗️</tg-emoji> <b>Update successful!</b> {emoji}\n\nRestarting in 2 seconds...'
	successMsg := sf(m.k, "updates", "update_success", map[string]interface{}{"emoji": face})
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, successMsg); err != nil {
		return err
	}

	time.Sleep(2 * time.Second)

	return m.k.Restart()
}

// ---------- .stop ----------

// cmdStop gracefully stops the userbot.
// Message format matches Python:
//
//	<tg-emoji ...>🧲</tg-emoji> <b>Your <i>MCUB</i> is stopping...</b> ʕ•ᴥ•ʔ
func (m *updatesModule) cmdStop(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	face := randomFace()
	// langpack stopping: '<tg-emoji ...>🧲</tg-emoji> <b>Your <i>{mcub}</i> is stopping...</b> {emoji}'
	msg := sf(m.k, "updates", "stopping", map[string]interface{}{
		"mcub":  m.mcubName(),
		"emoji": face,
	})
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, msg); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)
	m.k.Shutdown()
	return nil
}
