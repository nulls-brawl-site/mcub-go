// Package inline provides the Go port of MCUB's core_inline system.
// api.go ports core_inline/api/inline.py and core_inline/api/core.py.
//
// SPDX-License-Identifier: MIT
package inline

import (
	"encoding/json"

	"github.com/gotd/td/tg"
)

// callbackPayload is the JSON-encoded form stored in tg.KeyboardButtonCallback.Data.
type callbackPayload struct {
	Token string                 `json:"t"`
	Extra map[string]interface{} `json:"e,omitempty"`
}

// EncodeInlineData encodes a callback token and optional extra data into
// the byte slice written to tg.KeyboardButtonCallback.Data.
// Mirrors the build_inline_data helper from core_inline/api/inline.py.
func EncodeInlineData(token string, extra map[string]interface{}) []byte {
	p := callbackPayload{Token: token, Extra: extra}
	b, err := json.Marshal(p)
	if err != nil {
		// Fallback: just use the token as raw bytes.
		return []byte(token)
	}
	return b
}

// ParseInlineData decodes callback button data produced by EncodeInlineData.
// Returns the token and any extra fields. Falls back to treating the whole
// data slice as the token when JSON decoding fails (legacy raw token format).
func ParseInlineData(data []byte) (token string, extra map[string]interface{}) {
	var p callbackPayload
	if err := json.Unmarshal(data, &p); err == nil && p.Token != "" {
		return p.Token, p.Extra
	}
	// Legacy / raw token.
	return string(data), nil
}

// MakeCallbackButton creates a *tg.KeyboardButtonCallback that encodes token
// as its callback data.
//
// style and icon are accepted for API compatibility with the Python version
// but are not encoded in the MTProto button type (which has no icon field).
func MakeCallbackButton(text, token string, style string, icon int64) *tg.KeyboardButtonCallback {
	return &tg.KeyboardButtonCallback{
		Text: text,
		Data: EncodeInlineData(token, nil),
	}
}

// MakeURLButton creates a *tg.KeyboardButtonURL.
//
// style is accepted for API compatibility but has no MTProto equivalent.
func MakeURLButton(text, url string, style string) *tg.KeyboardButtonURL {
	return &tg.KeyboardButtonURL{
		Text: text,
		URL:  url,
	}
}

// BuildInlineKeyboard converts a [][]interface{} button grid into a
// *tg.ReplyInlineMarkup. Each element in the inner slices must implement
// tg.KeyboardButtonClass; non-conforming values are silently skipped.
func BuildInlineKeyboard(rows [][]interface{}) *tg.ReplyInlineMarkup {
	markup := &tg.ReplyInlineMarkup{}
	for _, row := range rows {
		kbRow := tg.KeyboardButtonRow{}
		for _, item := range row {
			if btn, ok := item.(tg.KeyboardButtonClass); ok {
				kbRow.Buttons = append(kbRow.Buttons, btn)
			}
		}
		if len(kbRow.Buttons) > 0 {
			markup.Rows = append(markup.Rows, kbRow)
		}
	}
	return markup
}

// BuildInlineKeyboardFromRows converts a slice of tg.KeyboardButtonRow into
// a *tg.ReplyInlineMarkup. Useful when rows are already in TL form.
func BuildInlineKeyboardFromRows(rows []tg.KeyboardButtonRow) *tg.ReplyInlineMarkup {
	return &tg.ReplyInlineMarkup{Rows: rows}
}
