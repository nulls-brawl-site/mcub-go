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
// All fields mirror the Python DEFAULT_CONFIG / ModuleConfig registration.
type APIProtectionConfig struct {
	// Core rate-limiting
	Enabled        bool    `json:"enable_protection"`
	TimeSample     int     `json:"time_sample"`
	LimitProfile   string  `json:"limit_profile"`
	Threshold      int     `json:"custom_threshold"`
	LocalFloodwait int     `json:"local_floodwait"`
	IgnoreMethods  []string `json:"ignore_methods"`

	// MCUB native protection
	Mode          string   `json:"mcub_mode"`
	DryRun        bool     `json:"mcub_dry_run"`
	MCUBAllowlist []string `json:"mcub_allowlist"`
	Lockdown      bool     `json:"mcub_lockdown"`

	// Analytics
	EnableAnalytics      bool    `json:"enable_analytics"`
	ZScoreThresh         float64 `json:"zscore_threshold"`
	WarnPercent          int     `json:"warn_percent"`
	PredictWindow        int     `json:"predict_window"`
	BaselineWindow       int     `json:"baseline_window"`
	ProfileMinSamples    int     `json:"profile_min_samples"`
	PredictAlertCooldown int     `json:"predict_alert_cooldown"`
	WarnAlertCooldown    int     `json:"warn_alert_cooldown"`
}

func defaultAPIConfig() APIProtectionConfig {
	return APIProtectionConfig{
		Enabled:              true,
		TimeSample:           30,
		LimitProfile:         "normal",
		Threshold:            200,
		LocalFloodwait:       30,
		IgnoreMethods:        []string{"GetMessagesRequest"},
		Mode:                 "safe",
		DryRun:               false,
		MCUBAllowlist:        []string{},
		Lockdown:             false,
		EnableAnalytics:      true,
		ZScoreThresh:         3.0,
		WarnPercent:          90,
		PredictWindow:        10,
		BaselineWindow:       300,
		ProfileMinSamples:    50,
		PredictAlertCooldown: 10,
		WarnAlertCooldown:    30,
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

	requests     []apiRequestEntry
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
		kern.Log.Warn("api_protection: could not load config: %v", err)
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
		{Name: "api_protection", Description: "show/configure API protection", Handler: m.cmdAPIProtection},
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
	case "custom":
		return m.cfg.Threshold
	default: // "normal"
		return 200
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

// .api_protection — show current status or enable/disable/set param
// Mirrors Python api_protection.py api_protection_handler:
//   no args  → show inline "Are you sure?" form (Go: show status panel)
//   on/off   → enable/disable, edit with lang["api_protection_enabled/disabled"]
//   <param> <value> → set config parameter via lang["api_param_set/error"]
func (m *apiProtModule) cmdAPIProtection(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	if len(args) == 0 {
		// Python shows "Are you sure?" inline form. Go shows status panel
		// (inline bot not available in the Go kernel).
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
	// Python: subcmd in ("on", "enable", "true") → lang["api_protection_enabled"]
	case "on", "enable", "true":
		m.cfg.Enabled = true
		m.saveConfig()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "api_protection", "api_protection_enabled"))

	// Python: subcmd in ("off", "disable", "false") → lang["api_protection_disabled"]
	case "off", "disable", "false":
		m.cfg.Enabled = false
		m.saveConfig()
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "api_protection", "api_protection_disabled"))

	default:
		// Python: len(args) >= 3 → .api_protection <param> <value>
		// supports any config key via type introspection
		if len(args) >= 2 {
			param := args[0]
			value := strings.Join(args[1:], " ")
			changed := m.setConfigParam(param, value)
			if changed {
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					sf(m.k, "api_protection", "api_param_set", map[string]interface{}{
						"param": param,
						"value": value,
					}))
			}
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				s(m.k, "api_protection", "api_param_error"))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "api_protection", "usage"))
	}
}

// setConfigParam sets a config param by name and returns true on success.
// Mirrors Python api_protection_handler param-setting branch.
func (m *apiProtModule) setConfigParam(param, value string) bool {
	switch param {
	case "enable_protection":
		m.cfg.Enabled = strings.ToLower(value) == "true" || value == "1" || strings.ToLower(value) == "yes"
	case "mcub_mode":
		if _, ok := apiModeDescriptions[value]; ok {
			m.cfg.Mode = value
		} else {
			return false
		}
	case "limit_profile":
		switch value {
		case "conservative", "normal", "aggressive", "custom":
			m.cfg.LimitProfile = value
		default:
			return false
		}
	case "custom_threshold":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return false
		}
		m.cfg.Threshold = n
	case "time_sample":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return false
		}
		m.cfg.TimeSample = n
	case "local_floodwait":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return false
		}
		m.cfg.LocalFloodwait = n
	case "mcub_dry_run":
		m.cfg.DryRun = strings.ToLower(value) == "true" || value == "1"
	case "mcub_lockdown":
		m.cfg.Lockdown = strings.ToLower(value) == "true" || value == "1"
	case "enable_analytics":
		m.cfg.EnableAnalytics = strings.ToLower(value) == "true" || value == "1"
	case "zscore_threshold":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return false
		}
		m.cfg.ZScoreThresh = f
	case "warn_percent":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 100 {
			return false
		}
		m.cfg.WarnPercent = n
	case "predict_window":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return false
		}
		m.cfg.PredictWindow = n
	case "baseline_window":
		n, err := strconv.Atoi(value)
		if err != nil || n < 10 {
			return false
		}
		m.cfg.BaselineWindow = n
	case "profile_min_samples":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return false
		}
		m.cfg.ProfileMinSamples = n
	case "predict_alert_cooldown":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return false
		}
		m.cfg.PredictAlertCooldown = n
	case "warn_alert_cooldown":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			return false
		}
		m.cfg.WarnAlertCooldown = n
	default:
		return false
	}
	m.saveConfig()
	return true
}

// .api_reset — reset counters and stats
// Python: lang["api_reset_done"]
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
		s(m.k, "api_protection", "api_reset_done"))
}

// .api_suspend <seconds> — temporarily suspend protection
// Python: lang["api_suspend"].format(seconds=seconds)
func (m *apiProtModule) cmdAPISuspend(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 || !isDigits(args[0]) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "api_protection", "usage"))
	}
	seconds, err := strconv.ParseFloat(args[0], 64)
	if err != nil || seconds <= 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "api_protection", "usage"))
	}
	until := float64(time.Now().UnixNano())/1e9 + seconds
	m.mu.Lock()
	m.suspendUntil = until
	m.mu.Unlock()
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		sf(m.k, "api_protection", "api_suspend", map[string]interface{}{
			"seconds": int(seconds),
		}))
}

// .lockdown — toggle lockdown mode
// Python outputs exactly:
//   ok_emoji = '<tg-emoji emoji-id="5368585403467048206">🪬</tg-emoji>'
//   f"{ok_emoji} Lockdown enabled" or f"{ok_emoji} Lockdown disabled"
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

	const lockdownEmoji = `<tg-emoji emoji-id="5368585403467048206">🪬</tg-emoji>`
	if m.cfg.Lockdown {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, lockdownEmoji+" Lockdown enabled")
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, lockdownEmoji+" Lockdown disabled")
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
