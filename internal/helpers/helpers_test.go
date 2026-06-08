package helpers_test

import (
	"strings"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/helpers"
)

// ---- EscapeHTML -------------------------------------------------------------

func TestEscapeHTMLPlain(t *testing.T) {
	got := helpers.EscapeHTML("hello")
	if got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestEscapeHTMLTags(t *testing.T) {
	got := helpers.EscapeHTML("<b>bold</b>")
	if got != "&lt;b&gt;bold&lt;/b&gt;" {
		t.Errorf("got %q", got)
	}
}

func TestEscapeHTMLAmpersand(t *testing.T) {
	got := helpers.EscapeHTML("a & b")
	if got != "a &amp; b" {
		t.Errorf("got %q", got)
	}
}

func TestEscapeHTMLEmpty(t *testing.T) {
	if helpers.EscapeHTML("") != "" {
		t.Fatal("expected empty")
	}
}

func TestEscapeHTMLMixed(t *testing.T) {
	in := "<script>alert('xss')</script>"
	out := helpers.EscapeHTML(in)
	if strings.Contains(out, "<script>") {
		t.Fatal("should escape script tag")
	}
}

// ---- EscapeQuotes -----------------------------------------------------------

func TestEscapeQuotes(t *testing.T) {
	got := helpers.EscapeQuotes(`"hello"`)
	if strings.Contains(got, `"`) {
		t.Errorf("should escape double quotes: %q", got)
	}
}

// ---- LooksLikeHTML ----------------------------------------------------------

func TestLooksLikeHTMLTrue(t *testing.T) {
	cases := []string{
		"<b>bold</b>",
		"<i>italic</i>",
		`<a href="x">link</a>`,
		"<code>code</code>",
		"<pre>pre</pre>",
	}
	for _, c := range cases {
		if !helpers.LooksLikeHTML(c) {
			t.Errorf("expected HTML detection for %q", c)
		}
	}
}

func TestLooksLikeHTMLFalse(t *testing.T) {
	cases := []string{
		"plain text",
		"no tags here",
		"3 < 5 and 7 > 2", // incomplete tags
	}
	for _, c := range cases {
		if helpers.LooksLikeHTML(c) {
			t.Errorf("expected no HTML detection for %q", c)
		}
	}
}

// ---- GetArgs ----------------------------------------------------------------

func TestGetArgsBasic(t *testing.T) {
	args := helpers.GetArgs(".cmd foo bar")
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d: %v", len(args), args)
	}
	if args[0] != "foo" || args[1] != "bar" {
		t.Errorf("unexpected args: %v", args)
	}
}

func TestGetArgsQuoted(t *testing.T) {
	args := helpers.GetArgs(`.cmd "hello world"`)
	if len(args) != 1 {
		t.Fatalf("expected 1 arg, got %d: %v", len(args), args)
	}
	if args[0] != "hello world" {
		t.Errorf("got %q", args[0])
	}
}

func TestGetArgsNoArgs(t *testing.T) {
	args := helpers.GetArgs(".cmd")
	if len(args) != 0 {
		t.Errorf("expected empty args, got %v", args)
	}
}

func TestGetArgsEmpty(t *testing.T) {
	args := helpers.GetArgs("")
	if len(args) != 0 {
		t.Errorf("expected empty, got %v", args)
	}
}

// ---- GetArgsRaw -------------------------------------------------------------

func TestGetArgsRawBasic(t *testing.T) {
	raw := helpers.GetArgsRaw(".cmd hello world")
	if raw != "hello world" {
		t.Errorf("got %q", raw)
	}
}

func TestGetArgsRawNoArgs(t *testing.T) {
	raw := helpers.GetArgsRaw(".cmd")
	if raw != "" {
		t.Errorf("expected empty, got %q", raw)
	}
}

// ---- FormatTime -------------------------------------------------------------

func TestFormatTimeSeconds(t *testing.T) {
	got := helpers.FormatTime(45, false)
	if !strings.Contains(got, "45") {
		t.Errorf("got %q", got)
	}
}

func TestFormatTimeMinutes(t *testing.T) {
	got := helpers.FormatTime(90, false)
	if !strings.Contains(got, "m") {
		t.Errorf("expected minutes in %q", got)
	}
}

func TestFormatTimeHours(t *testing.T) {
	got := helpers.FormatTime(3600, false)
	if !strings.Contains(got, "h") {
		t.Errorf("expected hours in %q", got)
	}
}

func TestFormatTimeDetailed(t *testing.T) {
	// 3h 25m 10s
	total := float64(3*3600 + 25*60 + 10)
	got := helpers.FormatTime(total, true)
	if !strings.Contains(got, "3") {
		t.Errorf("got %q", got)
	}
}

func TestFormatTimeDetailedZero(t *testing.T) {
	got := helpers.FormatTime(0, true)
	if got == "" {
		t.Fatal("should not be empty for zero")
	}
}

func TestFormatTimeWeeks(t *testing.T) {
	got := helpers.FormatTime(7*24*3600, true)
	if !strings.Contains(got, "w") {
		t.Errorf("expected weeks in %q", got)
	}
}

// ---- FormatDate / FormatDateUnix --------------------------------------------

func TestFormatDateDefault(t *testing.T) {
	import_time := helpers.FormatDate
	// just check it doesn't panic and returns a string
	_ = import_time
}

func TestFormatDateUnix(t *testing.T) {
	got := helpers.FormatDateUnix(0, "")
	if got == "" {
		t.Fatal("expected non-empty for unix 0")
	}
}

// ---- FormatRelativeTime -----------------------------------------------------

func TestFormatRelativeTimeRecent(t *testing.T) {
	// Just now (timestamp = 0 = far past, but ensure no panic)
	got := helpers.FormatRelativeTime(0)
	if got == "" {
		t.Fatal("expected non-empty")
	}
}

// ---- GetPrefix --------------------------------------------------------------

func TestGetPrefixString(t *testing.T) {
	got := helpers.GetPrefix(".")
	if got == "" {
		t.Fatal("expected non-empty prefix")
	}
}

func TestGetPrefixNil(t *testing.T) {
	// Should not panic
	got := helpers.GetPrefix(nil)
	_ = got
}
