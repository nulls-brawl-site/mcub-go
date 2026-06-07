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

// emojiFaces is a list of kaomoji used for stop/update messages.
var emojiFaces = []string{
	"ಠ_ಠ", "( ཀ ʖ̯ ཀ)", "(◕‿◕✿)", "(つ･･)つ", "༼つ◕_◕༽つ",
	"(•_•)", "☜(ﾟヮﾟ☜)", "(☞ﾟヮﾟ)☞", "ʕ•ᴥ•ʔ", "(づ￣ ³￣)づ",
	">_<", "0_o",
}

func randomFace() string {
	return emojiFaces[rand.Intn(len(emojiFaces))]
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

// ---------- .restart ----------

// cmdRestart sends a restarting message and re-execs the process.
func (m *updatesModule) cmdRestart(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	msg := fmt.Sprintf(
		`<blockquote>%s <i>Your <b>MCUB</b> is restarting...</i></blockquote>`,
		emojiTelescope,
	)
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, msg); err != nil {
		return err
	}

	return m.k.Restart()
}

// ---------- .update ----------

// cmdUpdate pulls the latest git changes and restarts the process.
func (m *updatesModule) cmdUpdate(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	// Show initial indicator.
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, "❄️"); err != nil {
		return err
	}

	branch := m.detectBranch()

	result, err := exec.Command("git", "pull", "origin", branch).CombinedOutput()
	if err != nil {
		errMsg := fmt.Sprintf(
			`%s <b>Error:</b> <code>%s</code>`,
			emojiError, err.Error(),
		)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, errMsg)
	}

	output := string(result)

	if strings.Contains(output, "Already up to date") {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf(`%s <b>Already latest version %s</b>`, emojiOKGreen, m.k.Version))
	}

	// Successful pull.
	outPreview := output
	if len(outPreview) > 200 {
		outPreview = outPreview[:200]
	}
	pullMsg := fmt.Sprintf(
		`%s <b>Git pull successful!</b>`+"\n\n"+`<code>%s</code>`,
		emojiAlembic, outPreview,
	)
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, pullMsg); err != nil {
		return err
	}

	time.Sleep(2 * time.Second)

	face := randomFace()
	successMsg := fmt.Sprintf(
		`%s <b>Update successful!</b> %s`+"\n\n"+`Restarting in 2 seconds...`,
		emojiAlembic, face,
	)
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, successMsg); err != nil {
		return err
	}

	time.Sleep(2 * time.Second)

	return m.k.Restart()
}

// ---------- .stop ----------

// cmdStop gracefully stops the userbot.
func (m *updatesModule) cmdStop(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}

	face := randomFace()
	msg := fmt.Sprintf(
		`%s <b>Your <i>MCUB</i> is stopping...</b> %s`,
		emojiMagnet, face,
	)
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, msg); err != nil {
		return err
	}

	time.Sleep(1 * time.Second)
	m.k.Shutdown()
	return nil
}
