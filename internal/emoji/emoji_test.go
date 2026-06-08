package emoji_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/emoji"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestIsEmojiTag
// ─────────────────────────────────────────────────────────────────────────────

func TestIsEmojiTag(t *testing.T) {
	validTags := []string{
		`<tg-emoji emoji-id="123">🔮</tg-emoji>`,
		`<tg-emoji emoji-id="9999999999">✨</tg-emoji>`,
		`<emoji document_id=123>🌟</emoji>`,
		`hello <tg-emoji emoji-id="1">x</tg-emoji> world`,
	}
	for _, tag := range validTags {
		if !emoji.IsEmojiTag(tag) {
			t.Errorf("IsEmojiTag(%q) = false, want true", tag)
		}
	}

	invalidTags := []string{
		"hello world",
		"<b>bold</b>",
		"<code>x</code>",
		"no emoji here",
		"",
	}
	for _, tag := range invalidTags {
		if emoji.IsEmojiTag(tag) {
			t.Errorf("IsEmojiTag(%q) = true, want false", tag)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestParseToEntities
// ─────────────────────────────────────────────────────────────────────────────

func TestParseToEntities(t *testing.T) {
	text := `<tg-emoji emoji-id="123456">🔮</tg-emoji>`
	plain, entities, err := emoji.ParseToEntities(text)
	if err != nil {
		t.Fatalf("ParseToEntities: %v", err)
	}
	if plain != "🔮" {
		t.Errorf("plain text: got %q, want %q", plain, "🔮")
	}
	if len(entities) != 1 {
		t.Fatalf("entity count: got %d, want 1", len(entities))
	}
}

func TestParseToEntities_NoEmoji(t *testing.T) {
	text := "plain text"
	plain, entities, err := emoji.ParseToEntities(text)
	if err != nil {
		t.Fatalf("ParseToEntities: %v", err)
	}
	if plain != text {
		t.Errorf("plain text unchanged: got %q, want %q", plain, text)
	}
	if len(entities) != 0 {
		t.Errorf("entity count: got %d, want 0", len(entities))
	}
}

func TestParseToEntities_LegacyTag(t *testing.T) {
	text := `<emoji document_id=987>✨</emoji>`
	plain, entities, err := emoji.ParseToEntities(text)
	if err != nil {
		t.Fatalf("ParseToEntities legacy: %v", err)
	}
	if plain != "✨" {
		t.Errorf("plain text: got %q, want %q", plain, "✨")
	}
	if len(entities) != 1 {
		t.Errorf("entity count: got %d, want 1", len(entities))
	}
}

func TestParseToEntities_Multiple(t *testing.T) {
	text := `<tg-emoji emoji-id="1">A</tg-emoji> and <tg-emoji emoji-id="2">B</tg-emoji>`
	plain, entities, err := emoji.ParseToEntities(text)
	if err != nil {
		t.Fatalf("ParseToEntities multiple: %v", err)
	}
	if plain != "A and B" {
		t.Errorf("plain text: got %q, want %q", plain, "A and B")
	}
	if len(entities) != 2 {
		t.Errorf("entity count: got %d, want 2", len(entities))
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRemoveEmojiTags
// ─────────────────────────────────────────────────────────────────────────────

func TestRemoveEmojiTags(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{
			input: `<tg-emoji emoji-id="1">🔮</tg-emoji>`,
			want:  "🔮",
		},
		{
			input: `hello <tg-emoji emoji-id="5">🌟</tg-emoji> world`,
			want:  "hello 🌟 world",
		},
		{
			input: `<emoji document_id=42>✨</emoji>`,
			want:  "✨",
		},
		{
			input: "no emoji here",
			want:  "no emoji here",
		},
	}
	for _, tt := range tests {
		got := emoji.RemoveEmojiTags(tt.input)
		if got != tt.want {
			t.Errorf("RemoveEmojiTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestExtractEmojiIDs
// ─────────────────────────────────────────────────────────────────────────────

func TestExtractEmojiIDs(t *testing.T) {
	text := `<tg-emoji emoji-id="111">A</tg-emoji> <tg-emoji emoji-id="222">B</tg-emoji> <tg-emoji emoji-id="111">C</tg-emoji>`
	ids := emoji.ExtractEmojiIDs(text)

	// Should deduplicate: 111 appears twice but must appear once.
	if len(ids) != 2 {
		t.Errorf("ExtractEmojiIDs count: got %d, want 2 (ids: %v)", len(ids), ids)
	}

	seen := make(map[int64]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate id %d in result", id)
		}
		seen[id] = true
	}
	if !seen[111] || !seen[222] {
		t.Errorf("expected IDs 111 and 222, got %v", ids)
	}
}

func TestExtractEmojiIDs_Empty(t *testing.T) {
	ids := emoji.ExtractEmojiIDs("no emoji")
	if len(ids) != 0 {
		t.Errorf("expected no IDs for plain text, got %v", ids)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestFormatEmoji
// ─────────────────────────────────────────────────────────────────────────────

func TestFormatEmoji(t *testing.T) {
	tag := emoji.FormatEmoji(12345, "🔮")
	expected := `<tg-emoji emoji-id="12345">🔮</tg-emoji>`
	if tag != expected {
		t.Errorf("FormatEmoji: got %q, want %q", tag, expected)
	}
}

func TestFormatEmojiRoundTrip(t *testing.T) {
	tag := emoji.FormatEmoji(99999, "⭐")
	ids := emoji.ExtractEmojiIDs(tag)
	if len(ids) != 1 || ids[0] != 99999 {
		t.Errorf("round-trip FormatEmoji/ExtractEmojiIDs: %v", ids)
	}
}
