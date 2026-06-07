package argparser_test

import (
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/argparser"
)

// ── ParseArgs ────────────────────────────────────────────────────────────────

func TestParseArgs(t *testing.T) {
	cmd, args := argparser.ParseArgs(".ping hello world", ".")
	if cmd != "ping" {
		t.Fatalf("expected cmd=ping, got %q", cmd)
	}
	if len(args) != 2 || args[0] != "hello" || args[1] != "world" {
		t.Fatalf("expected [hello world], got %v", args)
	}
}

func TestParseArgsNoArgs(t *testing.T) {
	cmd, args := argparser.ParseArgs(".ping", ".")
	if cmd != "ping" {
		t.Fatalf("expected ping, got %q", cmd)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args, got %v", args)
	}
}

func TestParseArgsWrongPrefix(t *testing.T) {
	cmd, args := argparser.ParseArgs("!ping hello", ".")
	if cmd != "" || args != nil {
		t.Fatalf("expected empty result for wrong prefix, got cmd=%q args=%v", cmd, args)
	}
}

func TestParseArgsQuoted(t *testing.T) {
	cmd, args := argparser.ParseArgs(`.echo "hello world" foo`, ".")
	if cmd != "echo" {
		t.Fatalf("expected echo, got %q", cmd)
	}
	if len(args) != 2 || args[0] != "hello world" || args[1] != "foo" {
		t.Fatalf("expected [hello world, foo], got %v", args)
	}
}

func TestParseArgsCustomPrefix(t *testing.T) {
	cmd, args := argparser.ParseArgs("!cmd a b", "!")
	if cmd != "cmd" {
		t.Fatalf("expected cmd, got %q", cmd)
	}
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %v", args)
	}
}

// ── GetArgsRaw ───────────────────────────────────────────────────────────────

func TestGetArgsRaw(t *testing.T) {
	raw := argparser.GetArgsRaw(".echo hello world", ".")
	if raw != "hello world" {
		t.Fatalf("expected 'hello world', got %q", raw)
	}
}

func TestGetArgsRawNoArgs(t *testing.T) {
	raw := argparser.GetArgsRaw(".echo", ".")
	if raw != "" {
		t.Fatalf("expected empty string, got %q", raw)
	}
}

func TestGetArgsRawWrongPrefix(t *testing.T) {
	raw := argparser.GetArgsRaw("hello world", ".")
	if raw != "" {
		t.Fatalf("expected empty string for wrong prefix, got %q", raw)
	}
}

// ── IsPipeline / ParsePipeline ───────────────────────────────────────────────

func TestIsPipeline(t *testing.T) {
	tests := []struct {
		text   string
		expect bool
	}{
		{".ping | .echo", true},
		{".ping && .echo", true},
		{".ping || .echo", true},
		{".ping", false},
		{".echo 'a | b'", false}, // inside quotes
	}
	for _, tc := range tests {
		got := argparser.IsPipeline(tc.text)
		if got != tc.expect {
			t.Errorf("IsPipeline(%q) = %v, want %v", tc.text, got, tc.expect)
		}
	}
}

func TestParsePipeline(t *testing.T) {
	segs := argparser.ParsePipeline(".ping | .echo hello && .done")
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d: %v", len(segs), segs)
	}
	if segs[0].Command != ".ping" || segs[0].Operator != "" {
		t.Errorf("seg[0]: %+v", segs[0])
	}
	if segs[1].Command != ".echo hello" || segs[1].Operator != "|" {
		t.Errorf("seg[1]: %+v", segs[1])
	}
	if segs[2].Command != ".done" || segs[2].Operator != "&&" {
		t.Errorf("seg[2]: %+v", segs[2])
	}
}

func TestParsePipelineSingle(t *testing.T) {
	segs := argparser.ParsePipeline(".cmd arg1 arg2")
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
}

// ── ParseFlags ───────────────────────────────────────────────────────────────

func TestParseFlags(t *testing.T) {
	// --verbose followed by a non-flag token: token is consumed as verbose's value.
	args := []string{"--output=file.txt", "--verbose", "-n", "5", "positional"}
	f := argparser.ParseFlags(args)

	if f.Strings["output"] != "file.txt" {
		t.Errorf("expected output=file.txt, got %q", f.Strings["output"])
	}
	// "--verbose" is followed by "-n" (starts with "-") → treated as bool flag
	if !f.Bools["verbose"] {
		t.Error("expected --verbose to be true (bool flag)")
	}
	if f.Strings["n"] != "5" {
		t.Errorf("expected -n 5, got %q", f.Strings["n"])
	}
	if len(f.Remaining) != 1 || f.Remaining[0] != "positional" {
		t.Errorf("expected [positional] remaining, got %v", f.Remaining)
	}
}

func TestParseFlagsCombinedShort(t *testing.T) {
	f := argparser.ParseFlags([]string{"-abc"})
	if !f.Bools["a"] || !f.Bools["b"] || !f.Bools["c"] {
		t.Errorf("expected combined short flags a,b,c; got %v", f.Bools)
	}
}

func TestParseFlagsEmpty(t *testing.T) {
	f := argparser.ParseFlags(nil)
	if len(f.Bools) != 0 || len(f.Strings) != 0 || len(f.Remaining) != 0 {
		t.Errorf("expected empty flags, got %+v", f)
	}
}

// ── SplitRespectingQuotes ─────────────────────────────────────────────────────

func TestSplitRespectingQuotes(t *testing.T) {
	tests := []struct {
		input  string
		expect []string
	}{
		{"hello world", []string{"hello", "world"}},
		{`"hello world" foo`, []string{"hello world", "foo"}},
		{`'hello world' bar`, []string{"hello world", "bar"}},
		{"one", []string{"one"}},
		{"", nil},
		{`a "b c" d`, []string{"a", "b c", "d"}},
	}
	for _, tc := range tests {
		got := argparser.SplitRespectingQuotes(tc.input)
		if len(got) != len(tc.expect) {
			t.Errorf("Split(%q) = %v, want %v", tc.input, got, tc.expect)
			continue
		}
		for i := range got {
			if got[i] != tc.expect[i] {
				t.Errorf("Split(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.expect[i])
			}
		}
	}
}

// ── ArgumentParser ────────────────────────────────────────────────────────────

func TestNewArgumentParser(t *testing.T) {
	ap, err := argparser.NewArgumentParser(".cmd arg1 --flag=val", ".")
	if err != nil {
		t.Fatal(err)
	}
	if ap.Command != "cmd" {
		t.Errorf("expected cmd, got %q", ap.Command)
	}
	if ap.Kwargs["flag"] != "val" {
		t.Errorf("expected flag=val, got %v", ap.Kwargs["flag"])
	}
}

func TestArgumentParserGet(t *testing.T) {
	ap, _ := argparser.NewArgumentParser(".cmd foo bar", ".")
	v := ap.Get(0, nil)
	if v != "foo" {
		t.Errorf("expected foo, got %v", v)
	}
	def := ap.Get(99, "default")
	if def != "default" {
		t.Errorf("expected default, got %v", def)
	}
}

func TestQuoteUnquote(t *testing.T) {
	s := argparser.Quote("hello world")
	if s != `"hello world"` {
		t.Errorf("Quote: expected %q, got %q", `"hello world"`, s)
	}
	u := argparser.Unquote(`"hello world"`)
	if u != "hello world" {
		t.Errorf("Unquote: expected hello world, got %q", u)
	}
}
