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
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji IDs used by the info module (exact from Python source).
const (
	emojiInfoStart     = `<tg-emoji emoji-id="5469913852462242978">🏓</tg-emoji>`  // load/start
	emojiArch          = `<tg-emoji emoji-id="5361837567463399422">🪩</tg-emoji>`
	emojiUbuntu        = `<tg-emoji emoji-id="5470088387048266598">🐉</tg-emoji>`
	emojiMint          = `<tg-emoji emoji-id="6021351236240938822">🚂</tg-emoji>`
	emojiFedora        = `<tg-emoji emoji-id="5888894642400795884">🛸</tg-emoji>`
	emojiCentos        = `<tg-emoji emoji-id="5938472510755444126">🧪</tg-emoji>`
	emojiVDS           = `<tg-emoji emoji-id="5471952986970267163">🧩</tg-emoji>`
	emojiWSL           = `<tg-emoji emoji-id="5395325195542078574">🍀</tg-emoji>`
	emojiTermux        = `<tg-emoji emoji-id="5300999883996536855">🌪️</tg-emoji>`
	emojiLightning     = `<tg-emoji emoji-id="5134201302888219205">🌩️</tg-emoji>`
	emojiHeartBroken   = `<tg-emoji emoji-id="4915853119839011973">💔</tg-emoji>`
	emojiCrystalBall   = `<tg-emoji emoji-id="5445259009311391329">🔮</tg-emoji>`
	emojiSatDish       = `<tg-emoji emoji-id="5289698618154955773">📡</tg-emoji>`
	emojiTestTube      = `<tg-emoji emoji-id="5208536646932253772">🧪</tg-emoji>`
	emojiMicroscope    = `<tg-emoji emoji-id="4904936030232117798">🔬</tg-emoji>`
	emojiDNA           = `<tg-emoji emoji-id="5368513458469878442">🧬</tg-emoji>`
	emojiBlueDiamond   = `<tg-emoji emoji-id="5406786135382845849">🔷</tg-emoji>`
	emojiOrangeDiamond = `<tg-emoji emoji-id="5406792732452613826">🔶</tg-emoji>`
	emojiPuzzle        = `<tg-emoji emoji-id="5332534105114445343">🧩</tg-emoji>`  // kernel line
	emojiGlobe         = `<tg-emoji emoji-id="4906943755644306322">🌐</tg-emoji>`
	emojiWarning       = `<tg-emoji emoji-id="5904692292324692386">⚠️</tg-emoji>`
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
		emoji = emojiArch
	case strings.Contains(lname, "ubuntu"):
		emoji = emojiUbuntu
	case strings.Contains(lname, "mint"):
		emoji = emojiMint
	case strings.Contains(lname, "fedora"):
		emoji = emojiFedora
	case strings.Contains(lname, "centos"):
		emoji = emojiCentos
	}
	return
}

// getPlatformType returns "VDS", "WSL", or "Termux" with a custom emoji.
func (m *infoModule) getPlatformType() string {
	// Termux: $PREFIX env var is set.
	if os.Getenv("PREFIX") != "" && strings.Contains(os.Getenv("PREFIX"), "com.termux") {
		return fmt.Sprintf("Termux %s", emojiTermux)
	}
	// WSL: /proc/version contains "microsoft".
	if data, err := os.ReadFile("/proc/version"); err == nil {
		if strings.Contains(strings.ToLower(string(data)), "microsoft") {
			return fmt.Sprintf("WSL %s", emojiWSL)
		}
	}
	return fmt.Sprintf("VDS %s", emojiVDS)
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
func (m *infoModule) cmdInfo(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}

	// Step 1: edit to start emoji to measure ping.
	start := time.Now()
	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, emojiInfoStart); err != nil {
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

	// Kernel type from Kernel.Type field.
	coreNameStr := string(m.k.Type)
	if coreNameStr == "" {
		coreNameStr = "standard"
	}

	// mcub_emoji: for non-premium users.
	mcubEmoji := "Mitrich UserBot"

	// Update check (may be slow — run in goroutine in production, but for now inline).
	updateNeeded := m.checkUpdateNeeded()

	var updateEmoji, updateText string
	if updateNeeded {
		updateEmoji = emojiHeartBroken
		updateText = "Update needed"
	} else {
		updateEmoji = emojiCrystalBall
		updateText = "No update needed"
	}

	branchDisplay := fmt.Sprintf(
		`%s<b> Branch: %s</b><b>#%s</b>`,
		emojiGlobe, branch, commitSHA,
	)

	infoText := fmt.Sprintf(
		`<b>%s</b>`+"\n"+
			`<blockquote>%s <b>Version:</b> <code>%s</code>`+"\n"+
			`%s <b>Kernel:</b> <code>%s</code>`+"\n"+
			`%s <b>%s</b>`+"\n"+
			`%s</blockquote>`+"\n\n"+
			`<blockquote>%s <b>Ping:</b> <code>%.2f ms</code>`+"\n"+
			`%s <b>Uptime:</b> <code>%s</code>`+"\n"+
			`%s <b>System:</b> %s %s`+"\n"+
			`%s <b>Platform:</b> <code>%s</code></blockquote>`+"\n\n"+
			`<blockquote>%s <b>CPU:</b> <i>~%s</i>`+"\n"+
			`%s <b>RAM:</b> <i>~%s</i></blockquote>`,
		mcubEmoji,
		emojiLightning, m.k.Version,
		emojiPuzzle, coreNameStr,
		updateEmoji, updateText,
		branchDisplay,
		emojiSatDish, pingMs,
		emojiTestTube, uptime,
		emojiMicroscope, distroName, distroEmoji,
		emojiDNA, platformType,
		emojiBlueDiamond, cpuUsage,
		emojiOrangeDiamond, ramUsage,
	)

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, infoText); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s <b>Error, see logs</b>", emojiWarning))
	}
	return nil
}
