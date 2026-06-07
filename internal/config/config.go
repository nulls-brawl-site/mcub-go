// Package config manages the MCUB userbot configuration file (config.json).
// Ported from core/lib/base/config.py (ConfigManager).
package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds all runtime configuration for the MCUB userbot.
// Fields map directly to config.json keys.
type Config struct {
	APIID               int64             `json:"api_id"`
	APIHash             string            `json:"api_hash"`
	Phone               string            `json:"phone"`
	CommandPrefix       string            `json:"command_prefix"`
	Aliases             map[string]string `json:"aliases"`
	PowerSaveMode       bool              `json:"power_save_mode"`
	TwoFAEnabled        bool              `json:"2fa_enabled"`
	HealthcheckInterval int               `json:"healthcheck_interval"`
	DeveloperChatID     *int64            `json:"developer_chat_id"`
	Language            string            `json:"language"`
	Theme               string            `json:"theme"`
	Proxy               *string           `json:"proxy"`
	InlineBotToken      *string           `json:"inline_bot_token"`
	InlineBotUsername   *string           `json:"inline_bot_username"`
	DBVersion           int               `json:"db_version"`
	WebPanelToken       *string           `json:"web_panel_token"`

	// Extended fields from config.py defaults.
	OwnerPrefixes        map[string]string          `json:"owner_prefixes,omitempty"`
	Piped                bool                       `json:"piped"`
	MemoryGuard          bool                       `json:"memory_guard"`
	WebPanelEnabled      bool                       `json:"web_panel_enabled"`
	WebPanelHost         string                     `json:"web_panel_host,omitempty"`
	WebPanelPort         int                        `json:"web_panel_port,omitempty"`
	LogChatID            *int64                     `json:"log_chat_id,omitempty"`
	LogBotEnabled        bool                       `json:"log_bot_enabled"`
	MaxReconnectAttempts int                        `json:"max_reconnect_attempts,omitempty"`
	ReconnectDelay       int                        `json:"reconnect_delay,omitempty"`
	ModuleConfigs        map[string]json.RawMessage `json:"module_configs,omitempty"`
}

// defaults returns a Config populated with sane default values.
func defaults() *Config {
	return &Config{
		CommandPrefix:        ".",
		Aliases:              map[string]string{},
		OwnerPrefixes:        map[string]string{},
		PowerSaveMode:        false,
		TwoFAEnabled:         false,
		HealthcheckInterval:  30,
		Language:             "en",
		Theme:                "default",
		DBVersion:            2,
		MaxReconnectAttempts: 10,
		ReconnectDelay:       5,
		ModuleConfigs:        map[string]json.RawMessage{},
	}
}

// Load reads and parses a config file from path.
// It returns an error if the file exists but cannot be read or is malformed.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer f.Close()

	cfg := defaults()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(cfg); err != nil {
		// Try again without strict unknown-field checking for forward compat.
		if _, seekErr := f.Seek(0, 0); seekErr != nil {
			return nil, fmt.Errorf("seek config %q: %w", path, seekErr)
		}
		cfg = defaults()
		if err2 := json.NewDecoder(f).Decode(cfg); err2 != nil {
			return nil, fmt.Errorf("decode config %q: %w", path, err2)
		}
	}

	if cfg.Aliases == nil {
		cfg.Aliases = map[string]string{}
	}
	if cfg.OwnerPrefixes == nil {
		cfg.OwnerPrefixes = map[string]string{}
	}
	if cfg.ModuleConfigs == nil {
		cfg.ModuleConfigs = map[string]json.RawMessage{}
	}

	return cfg, nil
}

// Save writes the config to path as indented JSON.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "    ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write config %q: %w", path, err)
	}
	return nil
}

// LoadOrCreate loads the config from path.
// If the file does not exist, it creates a default config at that path.
// The bool return value is true when the file was newly created.
func LoadOrCreate(path string) (*Config, bool, error) {
	cfg, err := Load(path)
	if err == nil {
		return cfg, false, nil
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		return nil, false, err
	}

	cfg = defaults()
	if saveErr := cfg.Save(path); saveErr != nil {
		return cfg, true, fmt.Errorf("save default config: %w", saveErr)
	}
	return cfg, true, nil
}

// Validate checks that the minimum required fields are set and have sensible
// types, mirroring ConfigManager._validate_config from config.py.
func (c *Config) Validate() error {
	if c.APIID == 0 {
		return fmt.Errorf("missing required field: api_id")
	}
	if c.APIHash == "" {
		return fmt.Errorf("missing required field: api_hash")
	}
	if c.Phone == "" {
		return fmt.Errorf("missing required field: phone")
	}
	if c.CommandPrefix == "" {
		return fmt.Errorf("command_prefix must not be empty")
	}
	return nil
}

// SetupFromInput runs an interactive CLI setup wizard that collects API_ID,
// API_HASH and phone number from the user, writes config.json to configPath,
// and returns the populated Config.
// Mirrors ConfigManager.first_time_setup from config.py.
func SetupFromInput(configPath string) (*Config, error) {
	r := bufio.NewReader(os.Stdin)

	readLine := func(prompt string) (string, error) {
		fmt.Print(prompt)
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}

	fmt.Println("\nMCUB Setup Wizard")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("1. Go to https://my.telegram.org and log in")
	fmt.Println("2. Click on \"API development tools\"")
	fmt.Println("3. Create a new application")
	fmt.Println("4. Copy your API ID and API hash")
	fmt.Println()

	var cfg *Config
	for {
		apiIDStr, err := readLine("API ID: ")
		if err != nil {
			return nil, fmt.Errorf("read API ID: %w", err)
		}
		apiID, err := strconv.ParseInt(apiIDStr, 10, 64)
		if err != nil || apiID <= 0 {
			fmt.Println("⚠  API ID must be a positive integer")
			continue
		}

		apiHash, err := readLine("API HASH: ")
		if err != nil {
			return nil, fmt.Errorf("read API hash: %w", err)
		}
		if apiHash == "" {
			fmt.Println("⚠  API HASH cannot be empty")
			continue
		}

		phone, err := readLine("Phone number (e.g. +1234567890): ")
		if err != nil {
			return nil, fmt.Errorf("read phone: %w", err)
		}
		if !strings.HasPrefix(phone, "+") {
			fmt.Println("⚠  Phone must start with + (e.g. +1234567890)")
			continue
		}

		cfg = defaults()
		cfg.APIID = apiID
		cfg.APIHash = apiHash
		cfg.Phone = phone
		break
	}

	if err := cfg.Save(configPath); err != nil {
		return nil, fmt.Errorf("save config: %w", err)
	}
	fmt.Println("✓  Config saved")
	return cfg, nil
}

// GetModuleConfig returns the per-module config map stored in ModuleConfigs.
// If no entry exists for moduleName the supplied defaults map is returned
// (or an empty map when defaults is nil).
func (c *Config) GetModuleConfig(moduleName string, defaults map[string]interface{}) (map[string]interface{}, error) {
	if c.ModuleConfigs == nil {
		if defaults != nil {
			return defaults, nil
		}
		return map[string]interface{}{}, nil
	}
	raw, ok := c.ModuleConfigs[moduleName]
	if !ok || len(raw) == 0 {
		if defaults != nil {
			return defaults, nil
		}
		return map[string]interface{}{}, nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("get module config %q: %w", moduleName, err)
	}
	return out, nil
}

// SetModuleConfig marshals data to JSON and stores it under moduleName in
// ModuleConfigs. The caller must call Save to persist to disk.
func (c *Config) SetModuleConfig(moduleName string, data map[string]interface{}) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal module config %q: %w", moduleName, err)
	}
	if c.ModuleConfigs == nil {
		c.ModuleConfigs = make(map[string]json.RawMessage)
	}
	c.ModuleConfigs[moduleName] = json.RawMessage(b)
	return nil
}

// DeleteModuleConfig removes the config entry for moduleName.
// The caller must call Save to persist the deletion.
func (c *Config) DeleteModuleConfig(moduleName string) error {
	if c.ModuleConfigs != nil {
		delete(c.ModuleConfigs, moduleName)
	}
	return nil
}

// GetKey returns a single key from the specified module's config.
// If the module or key is absent, defaultVal is returned.
func (c *Config) GetKey(moduleName, key string, defaultVal interface{}) (interface{}, error) {
	cfg, err := c.GetModuleConfig(moduleName, nil)
	if err != nil {
		return defaultVal, err
	}
	v, ok := cfg[key]
	if !ok {
		return defaultVal, nil
	}
	return v, nil
}

// SetKey sets a single key in the specified module's config.
// The caller must call Save to persist.
func (c *Config) SetKey(moduleName, key string, value interface{}) error {
	cfg, err := c.GetModuleConfig(moduleName, nil)
	if err != nil {
		return err
	}
	cfg[key] = value
	return c.SetModuleConfig(moduleName, cfg)
}

// GetAllModuleNames returns the names of all modules that have config stored
// in ModuleConfigs (non-empty entries only).
func (c *Config) GetAllModuleNames() []string {
	if len(c.ModuleConfigs) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.ModuleConfigs))
	for name, raw := range c.ModuleConfigs {
		if len(raw) > 0 {
			names = append(names, name)
		}
	}
	return names
}

// Merge merges keys from other into the top-level Config fields.
// Only the well-known string/bool/int fields are updated; unknown keys are
// stored in ModuleConfigs under the key name if the value is a map.
func (c *Config) Merge(other map[string]interface{}) {
	for k, v := range other {
		switch k {
		case "api_id":
			if id, ok := toInt64(v); ok {
				c.APIID = id
			}
		case "api_hash":
			if s, ok := v.(string); ok {
				c.APIHash = s
			}
		case "phone":
			if s, ok := v.(string); ok {
				c.Phone = s
			}
		case "command_prefix":
			if s, ok := v.(string); ok {
				c.CommandPrefix = s
			}
		case "language":
			if s, ok := v.(string); ok {
				c.Language = s
			}
		case "theme":
			if s, ok := v.(string); ok {
				c.Theme = s
			}
		case "power_save_mode":
			if b, ok := v.(bool); ok {
				c.PowerSaveMode = b
			}
		case "piped":
			if b, ok := v.(bool); ok {
				c.Piped = b
			}
		case "memory_guard":
			if b, ok := v.(bool); ok {
				c.MemoryGuard = b
			}
		case "web_panel_enabled":
			if b, ok := v.(bool); ok {
				c.WebPanelEnabled = b
			}
		case "log_bot_enabled":
			if b, ok := v.(bool); ok {
				c.LogBotEnabled = b
			}
		case "healthcheck_interval":
			if n, ok := toInt(v); ok {
				c.HealthcheckInterval = n
			}
		case "max_reconnect_attempts":
			if n, ok := toInt(v); ok {
				c.MaxReconnectAttempts = n
			}
		case "reconnect_delay":
			if n, ok := toInt(v); ok {
				c.ReconnectDelay = n
			}
		}
	}
}

// ToMap returns the Config as a flat map[string]interface{} using JSON
// round-trip so field names match the JSON tags.
func (c *Config) ToMap() map[string]interface{} {
	b, _ := json.Marshal(c)
	var out map[string]interface{}
	_ = json.Unmarshal(b, &out)
	return out
}

// toInt64 converts common numeric types (from JSON unmarshal) to int64.
func toInt64(v interface{}) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	}
	return 0, false
}

// toInt converts common numeric types to int.
func toInt(v interface{}) (int, bool) {
	n, ok := toInt64(v)
	return int(n), ok
}
