package modules

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/langpacks"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji IDs used by the info module (exact from MCUB_info.py CUSTOM_EMOJI).
const (
	infoEmojiLoad      = `<tg-emoji emoji-id="5469913852462242978">🏓</tg-emoji>`
	infoEmojiArch      = `<tg-emoji emoji-id="5361837567463399422">🪩</tg-emoji>`
	infoEmojiUbuntu    = `<tg-emoji emoji-id="5470088387048266598">🐉</tg-emoji>`
	infoEmojiMint      = `<tg-emoji emoji-id="6021351236240938822">🚂</tg-emoji>`
	infoEmojiFedora    = `<tg-emoji emoji-id="5888894642400795884">🛸</tg-emoji>`
	infoEmojiCentos    = `<tg-emoji emoji-id="5938472510755444126">🧪</tg-emoji>`
	infoEmojiVDS       = `<tg-emoji emoji-id="5471952986970267163">🧩</tg-emoji>`
	infoEmojiWSL       = `<tg-emoji emoji-id="5395325195542078574">🍀</tg-emoji>`
	infoEmojiTermux    = `<tg-emoji emoji-id="5300999883996536855">🌪️</tg-emoji>`
	infoEmojiThunder   = `<tg-emoji emoji-id="5134201302888219205">🌩️</tg-emoji>`
	infoEmojiHeart     = `<tg-emoji emoji-id="4915853119839011973">💔</tg-emoji>`
	infoEmojiOrb       = `<tg-emoji emoji-id="5445259009311391329">🔮</tg-emoji>`
	infoEmojiSatellite = `<tg-emoji emoji-id="5289698618154955773">📡</tg-emoji>`
	infoEmojiTest      = `<tg-emoji emoji-id="5208536646932253772">🧪</tg-emoji>`
	infoEmojiMicro     = `<tg-emoji emoji-id="4904936030232117798">🔬</tg-emoji>`
	infoEmojiDNA       = `<tg-emoji emoji-id="5368513458469878442">🧬</tg-emoji>`
	infoEmojiBlue      = `<tg-emoji emoji-id="5406786135382845849">🔷</tg-emoji>`
	infoEmojiOrange    = `<tg-emoji emoji-id="5406792732452613826">🔶</tg-emoji>`
	infoEmojiPuzzle    = `<tg-emoji emoji-id="5332534105114445343">🧩</tg-emoji>`
	infoEmojiGlobe     = `<tg-emoji emoji-id="4906943755644306322">🌐</tg-emoji>`
	infoEmojiWarning   = `<tg-emoji emoji-id="5904692292324692386">⚠️</tg-emoji>`
)

// infoModule implements the "MCUB_info" system module.
type infoModule struct {
	k *kernel.Kernel
}

func newInfoModule() loader.Module { return &infoModule{} }

// Name implements loader.Module.
func (m *infoModule) Name() string { return "MCUB_info" }

// OnLoad implements loader.Module.
func (m *infoModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("MCUB_info: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *infoModule) OnUnload(k interface{}) error {
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
func (m *infoModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "info", Description: "show system info", Handler: m.cmdInfo},
	}
}

// ---------- langpack helpers ----------

func (m *infoModule) lang() string {
	if m.k != nil {
		return m.k.GetLanguage()
	}
	return "en"
}

// errSeeLogsMsg returns the langpack error_see_logs string with {warning} substituted.
func (m *infoModule) errSeeLogsMsg() string {
	raw := langpacks.Default.Get(m.lang(), "mcub_info", "error_see_logs")
	return strings.NewReplacer("{warning}", infoEmojiWarning).Replace(raw)
}

// ---------- helpers ----------

// formatUptime formats a duration as "Xh Ym Zs".
func (m *infoModule) formatUptime(d time.Duration) string {
	total := int(d.Seconds())
	hours := total / 3600
	minutes := (total % 3600) / 60
	seconds := total % 60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// getDistro returns the OS distribution name and a matching custom emoji.
func (m *infoModule) getDistro() (name, emoji string) {
	name = "Linux"
	emoji = ""

	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				name = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
				break
			}
		}
	} else {
		name = runtime.GOOS
	}

	lname := strings.ToLower(name)
	switch {
	case strings.Contains(lname, "arch"):
		emoji = infoEmojiArch
	case strings.Contains(lname, "ubuntu"):
		emoji = infoEmojiUbuntu
	case strings.Contains(lname, "mint"):
		emoji = infoEmojiMint
	case strings.Contains(lname, "fedora"):
		emoji = infoEmojiFedora
	case strings.Contains(lname, "centos"):
		emoji = infoEmojiCentos
	}
	return
}

// getPlatformType returns "VDS", "WSL", or "Termux" with a custom emoji.
func (m *infoModule) getPlatformType() string {
	// Termux: $PREFIX env var is set.
	if os.Getenv("PREFIX") != "" && strings.Contains(os.Getenv("PREFIX"), "com.termux") {
		return fmt.Sprintf("Termux %s", infoEmojiTermux)
	}
	// WSL: /proc/version contains "microsoft".
	if data, err := os.ReadFile("/proc/version"); err == nil {
		if strings.Contains(strings.ToLower(string(data)), "microsoft") {
			return fmt.Sprintf("WSL %s", infoEmojiWSL)
		}
	}
	return fmt.Sprintf("VDS %s", infoEmojiVDS)
}

// getCPURAM returns CPU usage % and RAM usage % strings from /proc.
func (m *infoModule) getCPURAM() (cpu, ram string) {
	cpu = "N/A"
	ram = "N/A"

	// CPU from /proc/stat (single sample — not ideal but simple).
	if f, err := os.Open("/proc/stat"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "cpu ") {
				continue
			}
			var u, n, s, id, iow, irq, sirq int
			fmt.Sscanf(line, "cpu %d %d %d %d %d %d %d", &u, &n, &s, &id, &iow, &irq, &sirq)
			total := u + n + s + id + iow + irq + sirq
			used := total - id
			if total > 0 {
				cpu = fmt.Sprintf("%.1f%%", float64(used)/float64(total)*100)
			}
			break
		}
		f.Close()
	}

	// RAM from /proc/meminfo.
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var memTotal, memAvail int64
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fmt.Sscanf(line, "MemTotal: %d", &memTotal)
			} else if strings.HasPrefix(line, "MemAvailable:") {
				fmt.Sscanf(line, "MemAvailable: %d", &memAvail)
			}
		}
		if memTotal > 0 {
			used := memTotal - memAvail
			ram = fmt.Sprintf("%.1f%%", float64(used)/float64(memTotal)*100)
		}
	}
	return
}

// detectBranch returns the current git branch name.
func (m *infoModule) detectBranch() string {
	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err == nil {
		if b := strings.TrimSpace(string(out)); b != "" {
			return b
		}
	}
	return "main"
}

// detectCommitSHA returns the short git commit SHA.
func (m *infoModule) detectCommitSHA() string {
	out, err := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	if err == nil {
		if sha := strings.TrimSpace(string(out)); sha != "" {
			return sha
		}
	}
	return "unknown"
}

// detectCommitURL returns the GitHub commit URL for the current HEAD.
// Mirrors Python version_manager.get_github_commit_url().
func (m *infoModule) detectCommitURL(sha string) string {
	out, err := exec.Command("git", "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return ""
	}
	remote := strings.TrimSpace(string(out))
	// Convert SSH to HTTPS
	if strings.HasPrefix(remote, "git@github.com:") {
		remote = "https://github.com/" + strings.TrimPrefix(remote, "git@github.com:")
	}
	remote = strings.TrimSuffix(remote, ".git")
	if !strings.Contains(remote, "github.com") {
		return ""
	}
	return remote + "/commit/" + sha
}

// checkUpdateNeeded checks if there are unpulled commits on the remote.
func (m *infoModule) checkUpdateNeeded() bool {
	// Run git fetch with a short timeout.
	exec.Command("git", "fetch", "origin").Run() //nolint:errcheck
	out, err := exec.Command("git", "rev-list", "--count", "HEAD..@{u}").Output()
	if err != nil {
		return false
	}
	var count int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count)
	return count > 0
}

// ---------- .info ----------

// cmdInfo shows comprehensive system and userbot information.
// Output format matches Python MCUB_info.py _build_default_text exactly.
func (m *infoModule) cmdInfo(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}

	// Step 1: edit to start emoji to measure ping.
	start := time.Now()
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, infoEmojiLoad); err != nil {
		return err
	}
	pingMs := float64(time.Since(start).Microseconds()) / 1000.0

	// Step 2: gather system info.
	uptime := m.formatUptime(m.k.Uptime())
	distroName, distroEmoji := m.getDistro()
	platformType := m.getPlatformType()
	cpuUsage, ramUsage := m.getCPURAM()
	branch := m.detectBranch()
	commitSHA := m.detectCommitSHA()

	// Kernel type display tag (BOT, MINI, zen, standard).
	coreNameStr := m.k.GetKernelTag()

	// mcub_emoji: plain text for non-premium users (matches Python fallback).
	mcubEmoji := "Mitrich UserBot"

	// Update check.
	updateNeeded := m.checkUpdateNeeded()

	var updateEmoji, updateText string
	if updateNeeded {
		updateEmoji = infoEmojiHeart
		updateText = "Update needed"
	} else {
		updateEmoji = infoEmojiOrb
		updateText = "No update needed"
	}

	// Branch display — matches Python _build_default_text branch_display:
	// if commit_url: `{globe}<b> Branch: {branch}</b><b><a href="{url}">#{sha}</a></b>`
	// else:          `{globe}<b> Branch {branch}#{sha}</b>`
	commitURL := m.detectCommitURL(commitSHA)
	var branchDisplay string
	if commitURL != "" {
		branchDisplay = fmt.Sprintf(`%s<b> Branch: %s</b><b><a href="%s">#%s</a></b>`,
			infoEmojiGlobe, branch, commitURL, commitSHA)
	} else {
		branchDisplay = fmt.Sprintf(`%s<b> Branch %s#%s</b>`, infoEmojiGlobe, branch, commitSHA)
	}

	// Build info text matching Python _build_default_text format exactly:
	// <b>{mcub_emoji}</b>
	// <blockquote>{thunder} <b>Version:</b> <code>ver</code>
	// {puzzle} <b>Kernel:</b> <code>core</code>
	// {update_emoji} <b>{update_text}</b>
	// {branch_display}</blockquote>
	//
	// <blockquote>{satellite} <b>Ping:</b> <code>X ms</code>
	// {test} <b>Uptime:</b> <code>uptime</code>
	// {micro} <b>System:</b> distro distro_emoji
	// {dna} <b>Platform:</b> <code>platform</code></blockquote>
	//
	// <blockquote>{blue} <b>CPU:</b> <i>~cpu</i>
	// {orange} <b>RAM:</b> <i>~ram</i></blockquote>
	infoText := fmt.Sprintf(
		"<b>%s</b>\n"+
			"<blockquote>%s <b>Version:</b> <code>%s</code>\n"+
			"%s <b>Kernel:</b> <code>%s</code>\n"+
			"%s <b>%s</b>\n"+
			"%s</blockquote>\n\n"+
			"<blockquote>%s <b>Ping:</b> <code>%.2f ms</code>\n"+
			"%s <b>Uptime:</b> <code>%s</code>\n"+
			"%s <b>System:</b> %s %s\n"+
			"%s <b>Platform:</b> <code>%s</code></blockquote>\n\n"+
			"<blockquote>%s <b>CPU:</b> <i>~%s</i>\n"+
			"%s <b>RAM:</b> <i>~%s</i></blockquote>",
		mcubEmoji,
		infoEmojiThunder, m.k.Version,
		infoEmojiPuzzle, coreNameStr,
		updateEmoji, updateText,
		branchDisplay,
		infoEmojiSatellite, pingMs,
		infoEmojiTest, uptime,
		infoEmojiMicro, distroName, distroEmoji,
		infoEmojiDNA, platformType,
		infoEmojiBlue, cpuUsage,
		infoEmojiOrange, ramUsage,
	)

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, infoText); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, m.errSeeLogsMsg())
	}
	return nil
}
