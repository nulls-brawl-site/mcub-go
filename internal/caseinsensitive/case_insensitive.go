// Package caseinsensitive provides a map with case-insensitive string keys.
// Ported from core/lib/utils/case_insensitive.py.
// SPDX-License-Identifier: MIT
package caseinsensitive

import "strings"

// Dict is a map[string]interface{} that performs all key lookups
// case-insensitively, always preserving the original casing of the first
// insertion.
type Dict struct {
	data      map[string]interface{} // lower-key → value
	canonical map[string]string      // lower-key → original key
}

// New returns an empty Dict.
func New() *Dict {
	return &Dict{
		data:      make(map[string]interface{}),
		canonical: make(map[string]string),
	}
}

// Set stores value under key (case-insensitive).
func (d *Dict) Set(key string, value interface{}) {
	lower := strings.ToLower(key)
	d.data[lower] = value
	if _, exists := d.canonical[lower]; !exists {
		d.canonical[lower] = key
	}
}

// Get returns the value stored for key (case-insensitive) and whether it exists.
func (d *Dict) Get(key string) (interface{}, bool) {
	v, ok := d.data[strings.ToLower(key)]
	return v, ok
}

// GetDefault returns the value stored for key or def if absent.
func (d *Dict) GetDefault(key string, def interface{}) interface{} {
	if v, ok := d.Get(key); ok {
		return v
	}
	return def
}

// Delete removes the key from the dict.
func (d *Dict) Delete(key string) {
	lower := strings.ToLower(key)
	delete(d.data, lower)
	delete(d.canonical, lower)
}

// Has reports whether the key exists (case-insensitive).
func (d *Dict) Has(key string) bool {
	_, ok := d.data[strings.ToLower(key)]
	return ok
}

// Keys returns the original-casing keys in insertion order (order not guaranteed
// since Go maps are unordered).
func (d *Dict) Keys() []string {
	keys := make([]string, 0, len(d.canonical))
	for _, orig := range d.canonical {
		keys = append(keys, orig)
	}
	return keys
}

// Values returns all values.
func (d *Dict) Values() []interface{} {
	vals := make([]interface{}, 0, len(d.data))
	for _, v := range d.data {
		vals = append(vals, v)
	}
	return vals
}

// Items returns key-value pairs using original casing.
func (d *Dict) Items() [][2]interface{} {
	items := make([][2]interface{}, 0, len(d.data))
	for lower, v := range d.data {
		items = append(items, [2]interface{}{d.canonical[lower], v})
	}
	return items
}

// Len returns the number of entries.
func (d *Dict) Len() int {
	return len(d.data)
}

// Update merges src into d.
func (d *Dict) Update(src map[string]interface{}) {
	for k, v := range src {
		d.Set(k, v)
	}
}
