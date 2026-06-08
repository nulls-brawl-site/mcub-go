package htmlparser_test

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/htmlparser"
)

// ─────────────────────────────────────────────────────────────────────────────
// TestParseHTML
// ─────────────────────────────────────────────────────────────────────────────

func TestParseHTML(t *testing.T) {
	tests := []struct {
		name         string
		html         string
		wantText     string
		wantEntities int
	}{
		{
			name:         "bold",
			html:         "<b>bold</b>",
			wantText:     "bold",
			wantEntities: 1,
		},
		{
			name:         "italic",
			html:         "<i>italic</i>",
			wantText:     "italic",
			wantEntities: 1,
		},
		{
			name:         "code",
			html:         "<code>code</code>",
			wantText:     "code",
			wantEntities: 1,
		},
		{
			name:         "link",
			html:         `<a href="https://t.me">link</a>`,
			wantText:     "link",
			wantEntities: 1,
		},
		{
			name:         "mixed text and bold",
			html:         "hello <b>world</b>",
			wantText:     "hello world",
			wantEntities: 1,
		},
		{
			name:         "tg-spoiler",
			html:         "<tg-spoiler>spoiler</tg-spoiler>",
			wantText:     "spoiler",
			wantEntities: 1,
		},
		{
			name:         "blockquote",
			html:         "<blockquote>quote</blockquote>",
			wantText:     "quote",
			wantEntities: 1,
		},
		{
			name:         "underline",
			html:         "<u>under</u>",
			wantText:     "under",
			wantEntities: 1,
		},
		{
			name:         "strikethrough",
			html:         "<s>strike</s>",
			wantText:     "strike",
			wantEntities: 1,
		},
		{
			name:         "plain text",
			html:         "hello world",
			wantText:     "hello world",
			wantEntities: 0,
		},
		{
			name:         "br tag",
			html:         "line1<br>line2",
			wantText:     "line1\nline2",
			wantEntities: 0,
		},
		{
			name:         "custom emoji",
			html:         `<tg-emoji emoji-id="123">🔮</tg-emoji>`,
			wantText:     "🔮",
			wantEntities: 1,
		},
		{
			name:         "html entities",
			html:         "&amp;&lt;&gt;",
			wantText:     "&<>",
			wantEntities: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, entities, err := htmlparser.ParseHTML(tt.html)
			if err != nil {
				t.Fatalf("ParseHTML(%q): %v", tt.html, err)
			}
			if text != tt.wantText {
				t.Errorf("text: got %q, want %q", text, tt.wantText)
			}
			if len(entities) != tt.wantEntities {
				t.Errorf("entities count: got %d, want %d (entities: %v)", len(entities), tt.wantEntities, entities)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestUnparseHTML / TelegramToHTML
// ─────────────────────────────────────────────────────────────────────────────

func TestUnparseHTML(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		entities []tg.MessageEntityClass
		wantHTML string
	}{
		{
			name:     "plain text",
			text:     "hello",
			entities: nil,
			wantHTML: "hello",
		},
		{
			name: "bold",
			text: "hello",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityBold{Offset: 0, Length: 5},
			},
			wantHTML: "<b>hello</b>",
		},
		{
			name: "italic",
			text: "world",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityItalic{Offset: 0, Length: 5},
			},
			wantHTML: "<i>world</i>",
		},
		{
			name: "code",
			text: "snippet",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityCode{Offset: 0, Length: 7},
			},
			wantHTML: "<code>snippet</code>",
		},
		{
			name: "text url",
			text: "click",
			entities: []tg.MessageEntityClass{
				&tg.MessageEntityTextURL{Offset: 0, Length: 5, URL: "https://example.com"},
			},
			wantHTML: `<a href="https://example.com">click</a>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := htmlparser.TelegramToHTML(tt.text, tt.entities)
			if got != tt.wantHTML {
				t.Errorf("TelegramToHTML: got %q, want %q", got, tt.wantHTML)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// TestRoundTrip
// ─────────────────────────────────────────────────────────────────────────────

func TestRoundTrip(t *testing.T) {
	cases := []string{
		"<b>bold</b>",
		"<i>italic</i>",
		"<code>x = 1</code>",
		`<a href="https://t.me">link</a>`,
		"plain text only",
		"hello <b>world</b> and <i>more</i>",
	}

	for _, original := range cases {
		text, entities, err := htmlparser.ParseHTML(original)
		if err != nil {
			t.Errorf("ParseHTML(%q): %v", original, err)
			continue
		}
		rebuilt := htmlparser.TelegramToHTML(text, entities)
		// Round-trip check: parse the rebuilt HTML again.
		text2, entities2, err := htmlparser.ParseHTML(rebuilt)
		if err != nil {
			t.Errorf("ParseHTML(rebuilt %q): %v", rebuilt, err)
			continue
		}
		if text2 != text {
			t.Errorf("round-trip text mismatch for %q: %q → %q → %q", original, text, rebuilt, text2)
		}
		if len(entities2) != len(entities) {
			t.Errorf("round-trip entity count mismatch for %q: %d → %d", original, len(entities), len(entities2))
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers / utility tests
// ─────────────────────────────────────────────────────────────────────────────

func TestEscapeUnescapeHTML(t *testing.T) {
	input := `hello & "world" <test>`
	escaped := htmlparser.EscapeHTML(input)
	unescaped := htmlparser.UnescapeHTML(escaped)
	if unescaped != input {
		t.Errorf("escape/unescape round-trip: got %q, want %q", unescaped, input)
	}
}

func TestIsHTMLFormatted(t *testing.T) {
	if !htmlparser.IsHTMLFormatted("<b>bold</b>") {
		t.Error("should detect bold as HTML")
	}
	if !htmlparser.IsHTMLFormatted("<code>x</code>") {
		t.Error("should detect code as HTML")
	}
	if htmlparser.IsHTMLFormatted("plain text") {
		t.Error("plain text should not be HTML formatted")
	}
}

func TestStripHTML(t *testing.T) {
	got := htmlparser.StripHTML("<b>hello</b> <i>world</i>")
	if got != "hello world" {
		t.Errorf("StripHTML: got %q, want %q", got, "hello world")
	}
}
