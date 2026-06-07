// Package config manages the MCUB userbot configuration file (config.json).
package config

import (
	"encoding/json"
	"fmt"
	"os"
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
}

// defaults returns a Config populated with sane default values.
func defaults() *Config {
	return &Config{
		CommandPrefix:       ".",
		Aliases:             map[string]string{},
		PowerSaveMode:       false,
		TwoFAEnabled:        false,
		HealthcheckInterval: 30,
		Language:            "en",
		Theme:               "default",
		DBVersion:           2,
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
	if !os.IsNotExist(err) {
		// Unwrap to check the underlying error.
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			goto create
		}
		return nil, false, err
	}

create:
	cfg = defaults()
	if saveErr := cfg.Save(path); saveErr != nil {
		return cfg, true, fmt.Errorf("save default config: %w", saveErr)
	}
	return cfg, true, nil
}

// Validate checks that the minimum required fields are set.
func (c *Config) Validate() error {
	if c.APIID == 0 {
		return fmt.Errorf("api_id is required")
	}
	if c.APIHash == "" {
		return fmt.Errorf("api_hash is required")
	}
	if c.Phone == "" {
		return fmt.Errorf("phone is required")
	}
	if c.CommandPrefix == "" {
		return fmt.Errorf("command_prefix must not be empty")
	}
	return nil
}
