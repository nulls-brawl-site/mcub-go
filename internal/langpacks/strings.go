// SPDX-License-Identifier: MIT
// Package langpacks – Strings helper for per-module localised string access.
package langpacks

import (
	"bytes"
	"fmt"
	"text/template"
)

// Strings provides localised string access scoped to a specific module.
// Internally it delegates all lookups to a Manager with a fixed locale.
type Strings struct {
	manager *Manager
	module  string
	lang    string
	// cache holds the resolved module strings for the current locale.
	cache map[string]string
}

// New creates a Strings accessor for (module, lang) backed by Default.
func New(module, lang string) *Strings {
	return NewFrom(Default, module, lang)
}

// NewFrom creates a Strings accessor backed by a custom Manager.
func NewFrom(mgr *Manager, module, lang string) *Strings {
	s := &Strings{
		manager: mgr,
		module:  module,
		lang:    lang,
	}
	s.cache = mgr.GetModule(lang, module)
	return s
}

// Get retrieves a localised string by key.
// If args are provided they are passed to fmt.Sprintf.
// Falls back to the key itself if not found.
func (s *Strings) Get(key string, args ...interface{}) string {
	if val, ok := s.cache[key]; ok {
		if len(args) > 0 {
			return fmt.Sprintf(val, args...)
		}
		return val
	}
	return key
}

// MustGet returns the string for key, panicking if the key is absent.
func (s *Strings) MustGet(key string) string {
	if val, ok := s.cache[key]; ok {
		return val
	}
	panic(fmt.Sprintf("langpacks: missing key %q in module %q (lang %q)", key, s.module, s.lang))
}

// Format performs Go text/template substitution on the string for key.
// data is a map of template variables.
// Returns the key itself if not found.
func (s *Strings) Format(key string, data map[string]interface{}) string {
	raw, ok := s.cache[key]
	if !ok {
		return key
	}
	tmpl, err := template.New("").Parse(raw)
	if err != nil {
		return raw
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return raw
	}
	return buf.String()
}

// Has returns true if the key exists in the current locale.
func (s *Strings) Has(key string) bool {
	_, ok := s.cache[key]
	return ok
}

// Keys returns all available string keys for the current module and locale.
func (s *Strings) Keys() []string {
	keys := make([]string, 0, len(s.cache))
	for k := range s.cache {
		keys = append(keys, k)
	}
	return keys
}

// SetLang switches the active locale and reloads the string cache.
func (s *Strings) SetLang(lang string) {
	s.lang = lang
	s.cache = s.manager.GetModule(lang, s.module)
}
