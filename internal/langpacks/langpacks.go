// SPDX-License-Identifier: MIT
// Package langpacks provides language pack management for mcub-go.
// It reads embedded YAML files and provides localised string lookup
// with automatic fallback chains (locale -> base lang -> "en").
package langpacks

import (
	"embed"
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Embed all YAML files from the langpacks directory.
//
//go:embed *.yaml
var langpackFS embed.FS

// _globalMarker is the key used inside a string group to promote it to
// the __global__ namespace (merged into every module's strings).
const _globalMarker = "__global__"

// _globalModule is the synthetic module key that holds global strings.
const _globalModule = "__global__"

// _groupValue is the key inside a nested group dict that stores the plain
// string value of the group itself (i.e. the group can be both a string
// and a namespace).
const _groupValue = "__value__"

// LangPack holds all parsed strings for a single locale.
// The outer map key is the module name; the inner map is key→value.
// Values may themselves be maps (nested groups).
type LangPack struct {
	Lang    string
	// Strings is module-name → (key → raw value).
	// Raw value is either a string or map[string]interface{} for nested groups.
	Strings map[string]interface{}
}

// Manager manages all loaded language packs.
type Manager struct {
	mu       sync.RWMutex
	packs    map[string]*LangPack // locale → LangPack
	defaultL string
}

// Default is the package-level Manager, auto-populated from embedded YAML.
var Default = &Manager{}

func init() {
	if err := Default.LoadAll(); err != nil {
		// Non-fatal: log to stderr and continue without strings.
		fmt.Printf("[langpacks] warning: failed to load all packs: %v\n", err)
	}
}

// LoadAll loads every *.yaml file from the embedded FS.
func (m *Manager) LoadAll() error {
	entries, err := langpackFS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("langpacks: readdir: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.packs == nil {
		m.packs = make(map[string]*LangPack)
	}
	var firstErr error
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := langpackFS.ReadFile(e.Name())
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		lang := strings.TrimSuffix(e.Name(), ".yaml")
		if err := m.loadLocked(lang, data); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if m.defaultL == "" {
		m.defaultL = "en"
	}
	return firstErr
}

// Load parses a YAML payload and registers it under lang.
func (m *Manager) Load(lang string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.packs == nil {
		m.packs = make(map[string]*LangPack)
	}
	return m.loadLocked(lang, data)
}

// loadLocked parses the YAML and populates m.packs[lang]. Caller must hold m.mu.
func (m *Manager) loadLocked(lang string, data []byte) error {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("langpacks: parse %s: %w", lang, err)
	}

	pack := &LangPack{
		Lang:    lang,
		Strings: make(map[string]interface{}),
	}

	for moduleName, moduleVal := range raw {
		switch v := moduleVal.(type) {
		case map[string]interface{}:
			// Check for __global__ marker
			if isGlobalMarker(v[_globalMarker]) {
				// Promote into the __global__ module namespace
				global, _ := pack.Strings[_globalModule].(map[string]interface{})
				if global == nil {
					global = make(map[string]interface{})
				}
				inner := make(map[string]interface{})
				for k, val := range v {
					if k != _globalMarker {
						inner[k] = val
					}
				}
				global[moduleName] = inner
				pack.Strings[_globalModule] = global
			} else {
				pack.Strings[moduleName] = v
			}
		case string:
			// Top-level string metadata, e.g. "lang: en" (base language).
			pack.Strings[moduleName] = v
		default:
			// Skip unknown types.
		}
	}

	m.packs[lang] = pack
	return nil
}

// isGlobalMarker returns true when the value represents a truthy __global__ flag.
func isGlobalMarker(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		low := strings.ToLower(val)
		return low == "1" || low == "true" || low == "yes" || low == "on"
	case int:
		return val == 1
	}
	return false
}

// Available returns a sorted list of loaded locale codes.
func (m *Manager) Available() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.packs))
	for l := range m.packs {
		out = append(out, l)
	}
	return out
}

// SetDefault sets the default locale returned when no locale is specified.
func (m *Manager) SetDefault(lang string) {
	m.mu.Lock()
	m.defaultL = lang
	m.mu.Unlock()
}

// GetModule returns the raw string map for (locale, module), merged with globals.
// Falls back through the base-lang chain then "en".
func (m *Manager) GetModule(locale, module string) map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Build fallback chain: requested locale -> base lang -> "en"
	chain := m.fallbackChain(locale)
	for _, loc := range chain {
		pack, ok := m.packs[loc]
		if !ok {
			continue
		}
		raw, ok := pack.Strings[module]
		if !ok {
			continue
		}
		modMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		return m.mergeGlobals(pack, modMap)
	}
	return map[string]string{}
}

// Get retrieves a string for (locale, module, key) with optional fmt.Sprintf args.
// Falls back through locale chain then returns the key itself.
func (m *Manager) Get(locale, module, key string, args ...interface{}) string {
	mod := m.GetModule(locale, module)
	if val, ok := mod[key]; ok {
		if len(args) > 0 {
			return fmt.Sprintf(val, args...)
		}
		return val
	}
	return key
}

// Format retrieves a string and replaces {placeholder} tokens with values from data.
// Unmatched placeholders are left as-is.
func (m *Manager) Format(locale, module, key string, data map[string]interface{}) string {
	val := m.Get(locale, module, key)
	for k, v := range data {
		val = strings.ReplaceAll(val, "{"+k+"}", fmt.Sprintf("%v", v))
	}
	return val
}

// fallbackChain builds the locale resolution order.
// Caller must hold m.mu (read lock).
func (m *Manager) fallbackChain(locale string) []string {
	seen := map[string]bool{}
	chain := []string{}
	add := func(l string) {
		if l != "" && !seen[l] {
			seen[l] = true
			chain = append(chain, l)
		}
	}
	add(locale)
	// Check base lang declared in the locale's YAML (e.g. linux.yaml: lang: en)
	if pack, ok := m.packs[locale]; ok {
		if baseLang, ok := pack.Strings["lang"].(string); ok {
			add(baseLang)
		}
	}
	add("ru")
	add("en")
	return chain
}

// mergeGlobals merges __global__ strings into a module string map.
// Caller must hold m.mu (read lock).
func (m *Manager) mergeGlobals(pack *LangPack, modMap map[string]interface{}) map[string]string {
	result := map[string]string{}

	// Seed with global strings first.
	if globalRaw, ok := pack.Strings[_globalModule]; ok {
		if globalMap, ok := globalRaw.(map[string]interface{}); ok {
			for _, groupVal := range globalMap {
				if groupMap, ok := groupVal.(map[string]interface{}); ok {
					flattenInto(result, "", groupMap)
				}
			}
		}
	}

	// Overlay with module-specific strings (they win).
	flattenInto(result, "", modMap)
	return result
}

// flattenInto recursively flattens a nested map into the output map.
// Nested keys are joined with ".".
func flattenInto(out map[string]string, prefix string, m map[string]interface{}) {
	for k, v := range m {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}
		switch val := v.(type) {
		case string:
			out[fullKey] = val
		case map[string]interface{}:
			// Also expose the group value if present.
			if sv, ok := val[_groupValue].(string); ok {
				out[fullKey] = sv
			}
			flattenInto(out, fullKey, val)
		default:
			if val != nil {
				out[fullKey] = fmt.Sprintf("%v", val)
			}
		}
	}
}
