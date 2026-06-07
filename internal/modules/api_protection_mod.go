package modules

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

const dbKeyAPIProtConfig = "api_protection:config"

// APIModeInfo describes protection behaviour per mode.
var apiModeDescriptions = map[string]string{
	"off":    "No protection active",
	"safe":   "Block dangerous mutations (safe mode)",
	"strict": "Block all non-read requests",
	"custom": "Custom policy (lockdown or allowlist)",
}

// APIProtectionConfig holds the persisted configuration for this module.
type APIProtectionConfig struct {
	Enabled        bool    `json:"enable_protection"`
	Mode           string  `json:"mcub_mode"`
	TimeSample     int     `json:"time_sample"`
	LocalFloodwait int     `json:"local_floodwait"`
	Threshold      int     `json:"custom_threshold"`
	DryRun         bool    `json:"mcub_dry_run"`
	Lockdown       bool    `json:"mcub_lockdown"`
	ZScoreThresh   float64 `json:"zscore_threshold"`
	LimitProfile   string  `json:"limit_profile"`
}

func defaultAPIConfig() APIProtectionConfig {
	return APIProtectionConfig{
		Enabled:        true,
		Mode:           "safe",
		TimeSample:     30,
		LocalFloodwait: 30,
		Threshold:      200,
		DryRun:         false,
		Lockdown:       false,
		ZScoreThresh:   3.0,
		LimitProfile:   "normal",
	}
}

// apiRequestEntry records a single API request for rate tracking.
type apiRequestEntry struct {
	Method string
	TS     float64
}

// apiProtModule implements the API protection module.
type apiProtModule struct {
	k   *kernel.Kernel
	mu  sync.Mutex
	cfg APIProtectionConfig

	requests    []apiRequestEntry
	blockedUntil float64
	suspendUntil float64
	triggerCount int
}

func newAPIProtModule() loader.Module { return &apiProtModule{cfg: defaultAPIConfig()} }

func (m *apiProtModule) Name() string { return "api_protection" }

func (m *apiProtModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("api_protection: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	// Load config from DB.
	cfg := defaultAPIConfig()
	err := kern.DB.GetJSON(dbKeyAPIProtConfig, &cfg)
	if err != nil && err != sql.ErrNoRows {
		kern.Log.Warn("api_protection: could not load config: " + err.Error())
	}
	m.cfg = cfg

	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

func (m *apiProtModule) OnUnload(k interface{}) error {
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

func (m *apiProtModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "api_protection", Description: "show/configure API protection status", Handler: m.cmdAPIProtection},
		{Name: "api_reset", Description: "reset API protection stats and counters", Handler: m.cmdAPIReset},
		{Name: "api_suspend", Description: "<seconds> temporarily suspend API protection", Handler: m.cmdAPISuspend},
		{Name: "lockdown", Description: "toggle lockdown mode (blocks profile edits, chat creation, etc.)", Handler: m.cmdLockdown},
	}
}

// ---------- helpers ----------

func (m *apiProtModule) saveConfig() {
	if m.k != nil {
		_ = m.k.DB.SetJSON(dbKeyAPIProtConfig, m.cfg)
	}
}

func (m *apiProtModule) parseArgs(ev *events.NewMessage) []string {
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

// recentCount returns the count of requests in the last n seconds.
func (m *apiProtModule) recentCount(seconds float64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := float64(time.Now().UnixNano()) / 1e9
	cutoff := now - seconds
	n := 0
	for _, r := range m.requests {
		if r.TS > cutoff {
			n++
		}
	}
	return n
}

// profileThreshold returns the request threshold based on the limit profile.
func (m *apiProtModule) profileThreshold() int {
	switch m.cfg.LimitProfile {
	case "conservative":
		return 100
	case "aggressive":
		return 350
	default:
		return m.cfg.Threshold
	}
}

// statusIcon returns ✅/🚨 based on current vs threshold.
func statusIcon(current, threshold int) string {
	pct := 0
	if threshold > 0 {
		pct = current * 100 / threshold
	}
	switch {
	case pct >= 100:
		return "🚨"
	case pct >= 90:
		return "⚠️"
	default:
		return "✅"
	}
}

// ---------- command handlers ----------

// .api_protection — show current status or toggle on/off
func (m *apiProtModule) cmdAPIProtection(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	if len(args) == 0 {
		// Show status.
		now := float64(time.Now().UnixNano()) / 1e9
		sample := float64(m.cfg.TimeSample)
		recent := m.recentCount(sample)
		threshold := m.profileThreshold()
		icon := statusIcon(recent, threshold)

		suspended := ""
		m.mu.Lock()
		if m.suspendUntil > now {
			suspended = fmt.Sprintf("\nSuspended: %.0fs remaining", m.suspendUntil-now)
		}
		blocked := ""
		if m.blockedUntil > now {
			blocked = fmt.Sprintf("\nBlocked: %.0fs remaining", m.blockedUntil-now)
		}
		triggers := m.triggerCount
		m.mu.Unlock()

		modeDesc := apiModeDescriptions[m.cfg.Mode]
		enabledStr := "✅ enabled"
		if !m.cfg.Enabled {
			enabledStr = "🚫 disabled"
		}
		lockdownStr := ""
		if m.cfg.Lockdown {
			lockdownStr = "\nLockdown: 🔒 active"
		}

		msg := fmt.Sprintf(
			"🛡️ <b>API Protection</b>\n"+
				"<blockquote>Status: %s\n"+
				"Mode: <code>%s</code> — %s\n"+
				"Requests/%ds: <code>%d</code>/%d %s\n"+
				"Triggers: <code>%d</code>%s%s%s</blockquote>",
			enabledStr,
			m.cfg.Mode, modeDesc,
			m.cfg.TimeSample, recent, threshold, icon,
			triggers,
			suspended, blocked, lockdownStr,
		)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, msg)
	}

	subcmd := strings.ToLower(args[0])
	switch subcmd {
	case "on", "enable", "true":
		m.cfg.Enabled = true
		m.saveConfig()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"🛡️ API protection <b>enabled</b>")
	case "off", "disable", "false":
		m.cfg.Enabled = false
		m.saveConfig()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"🛡️ API protection <b>disabled</b>")
	case "mode":
		if len(args) < 2 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.api_protection mode off|safe|strict|custom</code>")
		}
		mode := strings.ToLower(args[1])
		if _, ok := apiModeDescriptions[mode]; !ok {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ Invalid mode. Choose: off, safe, strict, custom")
		}
		m.cfg.Mode = mode
		m.cfg.Enabled = true
		m.saveConfig()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🛡️ Mode set to <code>%s</code>: %s", mode, apiModeDescriptions[mode]))
	case "threshold":
		if len(args) < 2 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.api_protection threshold &lt;number&gt;</code>")
		}
		n, err := strconv.Atoi(args[1])
		if err != nil || n <= 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ Threshold must be a positive integer")
		}
		m.cfg.Threshold = n
		m.cfg.LimitProfile = "custom"
		m.saveConfig()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("✅ Threshold set to <code>%d</code> req/%ds", n, m.cfg.TimeSample))
	default:
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.api_protection [on|off|mode|threshold] [value]</code>")
	}
}

// .api_reset — reset counters and stats
func (m *apiProtModule) cmdAPIReset(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	m.mu.Lock()
	m.requests = nil
	m.blockedUntil = 0
	m.suspendUntil = 0
	m.triggerCount = 0
	m.mu.Unlock()
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		"✅ <b>API protection stats reset</b>")
}

// .api_suspend <seconds> — temporarily suspend protection
func (m *apiProtModule) cmdAPISuspend(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 || !isDigits(args[0]) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.api_suspend &lt;seconds&gt;</code>")
	}
	seconds, err := strconv.ParseFloat(args[0], 64)
	if err != nil || seconds <= 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ Seconds must be a positive number")
	}
	until := float64(time.Now().UnixNano())/1e9 + seconds
	m.mu.Lock()
	m.suspendUntil = until
	m.mu.Unlock()
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("⏸️ API protection <b>suspended</b> for <code>%.0f</code> seconds", seconds))
}

// .lockdown — toggle lockdown mode
func (m *apiProtModule) cmdLockdown(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	m.cfg.Lockdown = !m.cfg.Lockdown
	if m.cfg.Lockdown {
		m.cfg.Mode = "custom"
		m.cfg.Enabled = true
	}
	m.saveConfig()
	if m.cfg.Lockdown {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"🔒 <b>Lockdown enabled</b>\n<i>Profile edits, chat creation and other mutations are blocked.</i>")
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		"🔓 <b>Lockdown disabled</b>")
}

// isDigits returns true if s contains only decimal digits (optionally with a leading minus).
func isDigits(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i, c := range s {
		if c == '-' && i == 0 {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}


