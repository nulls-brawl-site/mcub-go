// Package placeholders provides a dynamic text placeholder registry.
// Ported from utils/custom_placeholders.py.
// SPDX-License-Identifier: MIT
package placeholders

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	keyRE   = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	tokenRE = regexp.MustCompile(`\{([A-Za-z0-9_]+)\}`)
)

// OnError controls how a placeholder handles getter failures.
type OnError string

const (
	OnErrorKeep  OnError = "keep"  // leave the {token} as-is
	OnErrorEmpty OnError = "empty" // replace with ""
	OnErrorRaise OnError = "raise" // propagate the error
)

// Placeholder is a named dynamic value provider.
type Placeholder struct {
	Name        string
	Description string
	Getter      func(data map[string]interface{}) (string, error)
	Module      string // scope / module name
	Timeout     time.Duration
	CacheTTL    time.Duration
	Required    bool
	OnError     OnError

	mu         sync.Mutex
	cachedVal  string
	cachedAt   time.Time
}

// Registry stores all registered placeholders organised by scope/module.
type Registry struct {
	mu    sync.RWMutex
	items map[string]map[string]*Placeholder // scope → key → Placeholder
}

// Global is the default process-wide registry.
var Global = NewRegistry()

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{items: make(map[string]map[string]*Placeholder)}
}

func validateKey(key string) error {
	if !keyRE.MatchString(key) {
		return fmt.Errorf("invalid placeholder key %q: use letters, digits, underscore", key)
	}
	return nil
}

// Register adds or replaces a placeholder in the given scope.
func (r *Registry) Register(scope, key, description string, getter func(data map[string]interface{}) (string, error), opts ...func(*Placeholder)) {
	if err := validateKey(key); err != nil {
		return
	}
	ph := &Placeholder{
		Name:        key,
		Description: description,
		Getter:      getter,
		Module:      scope,
		OnError:     OnErrorKeep,
	}
	for _, o := range opts {
		o(ph)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.items[scope] == nil {
		r.items[scope] = make(map[string]*Placeholder)
	}
	r.items[scope][key] = ph
}

// WithTimeout returns a Register option that sets a getter timeout.
func WithTimeout(d time.Duration) func(*Placeholder) {
	return func(p *Placeholder) { p.Timeout = d }
}

// WithCacheTTL returns a Register option that caches getter results.
func WithCacheTTL(d time.Duration) func(*Placeholder) {
	return func(p *Placeholder) { p.CacheTTL = d }
}

// WithRequired marks the placeholder as required.
func WithRequired(p *Placeholder) { p.Required = true }

// WithOnError sets the on-error behaviour.
func WithOnError(e OnError) func(*Placeholder) {
	return func(p *Placeholder) { p.OnError = e }
}

// UnregisterScope removes all placeholders registered under scope.
func (r *Registry) UnregisterScope(scope string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.items[scope])
	delete(r.items, scope)
	return n
}

// UnregisterKey removes a single placeholder.
func (r *Registry) UnregisterKey(scope, key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	m := r.items[scope]
	if m == nil {
		return false
	}
	if _, ok := m[key]; !ok {
		return false
	}
	delete(m, key)
	if len(m) == 0 {
		delete(r.items, scope)
	}
	return true
}

// find looks for key in scope, then "global", then any scope.
func (r *Registry) find(scope, key string) *Placeholder {
	if m := r.items[scope]; m != nil {
		if ph := m[key]; ph != nil {
			return ph
		}
	}
	if m := r.items["global"]; m != nil {
		if ph := m[key]; ph != nil {
			return ph
		}
	}
	for s, m := range r.items {
		if s == scope || s == "global" {
			continue
		}
		if ph := m[key]; ph != nil {
			return ph
		}
	}
	return nil
}

// invoke calls the getter with an optional timeout.
func invoke(ph *Placeholder, data map[string]interface{}) (string, error) {
	// Check cache.
	if ph.CacheTTL > 0 {
		ph.mu.Lock()
		if ph.cachedVal != "" && time.Since(ph.cachedAt) < ph.CacheTTL {
			v := ph.cachedVal
			ph.mu.Unlock()
			return v, nil
		}
		ph.mu.Unlock()
	}

	type result struct {
		val string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := ph.Getter(data)
		ch <- result{v, err}
	}()

	var res result
	if ph.Timeout > 0 {
		select {
		case res = <-ch:
		case <-time.After(ph.Timeout):
			return "", fmt.Errorf("placeholder %q getter timed out", ph.Name)
		}
	} else {
		res = <-ch
	}

	if res.err != nil {
		return "", res.err
	}

	// Store cache.
	if ph.CacheTTL > 0 {
		ph.mu.Lock()
		ph.cachedVal = res.val
		ph.cachedAt = time.Now()
		ph.mu.Unlock()
	}
	return res.val, nil
}

// Resolve replaces {token} occurrences in template with resolved values.
// data provides pre-resolved values; any token not in data is looked up in the registry.
// customValues override registry lookups.
func (r *Registry) Resolve(scope, template string, data map[string]interface{}, customValues map[string]interface{}, strict bool) (string, error) {
	if data == nil {
		data = make(map[string]interface{})
	}
	if customValues == nil {
		customValues = make(map[string]interface{})
	}

	tokens := make(map[string]struct{})
	for _, m := range tokenRE.FindAllStringSubmatch(template, -1) {
		tokens[m[1]] = struct{}{}
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for token := range tokens {
		if _, ok := data[token]; ok {
			continue
		}
		if v, ok := customValues[token]; ok {
			data[token] = fmt.Sprintf("%v", v)
			continue
		}
		ph := r.find(scope, token)
		if ph == nil {
			if strict {
				return "", fmt.Errorf("unknown placeholder: {%s}", token)
			}
			continue
		}
		val, err := invoke(ph, data)
		if err != nil {
			switch ph.OnError {
			case OnErrorRaise:
				return "", err
			case OnErrorEmpty:
				data[token] = ""
			// OnErrorKeep: leave token unreplaced
			}
			continue
		}
		data[token] = val
	}

	// Check required.
	if strict {
		r.mu.RUnlock() // temporarily unlock to avoid nested lock confusion
		// (already holding read lock above — just check data)
		r.mu.RLock()
		if m := r.items[scope]; m != nil {
			for key, ph := range m {
				if ph.Required && strings.Contains(template, "{"+key+"}") {
					if _, ok := data[key]; !ok {
						return "", fmt.Errorf("missing required placeholder: {%s}", key)
					}
				}
			}
		}
	}

	result := tokenRE.ReplaceAllStringFunc(template, func(s string) string {
		token := s[1 : len(s)-1]
		if v, ok := data[token]; ok {
			return fmt.Sprintf("%v", v)
		}
		if strict {
			return s // caller will see the error from above
		}
		return s
	})
	return result, nil
}

// Format returns a human-readable list of placeholders available in scope.
func (r *Registry) Format(scope string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := r.items[scope]
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		ph := m[k]
		desc := ph.Description
		if desc == "" {
			desc = "No docs"
		}
		sb.WriteString(fmt.Sprintf("{%s} - %s\n", k, desc))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// List returns all placeholder keys across all scopes (sorted).
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, m := range r.items {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ListScope returns all placeholder keys for a specific scope (sorted).
func (r *Registry) ListScope(scope string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m := r.items[scope]
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
