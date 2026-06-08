package colors_test

import (
	"strings"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/colors"
)

// ---- Paint ------------------------------------------------------------------

func TestPaintContainsText(t *testing.T) {
	result := colors.Paint("hello", colors.Bold, colors.BrightGreen)
	if !strings.Contains(result, "hello") {
		t.Fatalf("Paint result %q does not contain text", result)
	}
}

func TestPaintContainsReset(t *testing.T) {
	result := colors.Paint("hello", colors.Bold)
	if !strings.Contains(result, colors.Reset) {
		t.Fatalf("Paint result %q missing Reset", result)
	}
}

func TestPaintNoCodes(t *testing.T) {
	result := colors.Paint("hi")
	if !strings.Contains(result, "hi") {
		t.Fatal("missing text")
	}
}

func TestBoldText(t *testing.T) {
	r := colors.BoldText("hello")
	if !strings.Contains(r, colors.Bold) {
		t.Fatal("missing Bold code")
	}
	if !strings.Contains(r, "hello") {
		t.Fatal("missing text")
	}
}

func TestErrorText(t *testing.T) {
	r := colors.ErrorText("oops")
	if !strings.Contains(r, "oops") {
		t.Fatal("missing text")
	}
	if !strings.Contains(r, colors.BrightRed) {
		t.Fatal("missing BrightRed")
	}
}

func TestSuccessText(t *testing.T) {
	r := colors.SuccessText("ok")
	if !strings.Contains(r, "ok") {
		t.Fatal("missing text")
	}
}

func TestWarningText(t *testing.T) {
	r := colors.WarningText("warn")
	if !strings.Contains(r, "warn") {
		t.Fatal("missing text")
	}
}

func TestInfoText(t *testing.T) {
	r := colors.InfoText("info")
	if !strings.Contains(r, "info") {
		t.Fatal("missing text")
	}
}

// ---- StripANSI --------------------------------------------------------------

func TestStripANSIPlain(t *testing.T) {
	got := colors.StripANSI("hello")
	if got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestStripANSIColored(t *testing.T) {
	painted := colors.Paint("world", colors.Red)
	got := colors.StripANSI(painted)
	if got != "world" {
		t.Errorf("StripANSI(%q) = %q, want %q", painted, got, "world")
	}
}

func TestStripANSIBold(t *testing.T) {
	s := colors.Bold + "bold" + colors.Reset
	got := colors.StripANSI(s)
	if got != "bold" {
		t.Errorf("got %q, want %q", got, "bold")
	}
}

func TestStripANSIEmpty(t *testing.T) {
	got := colors.StripANSI("")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ---- RGB / Hex --------------------------------------------------------------

func TestRGB(t *testing.T) {
	esc := colors.RGB(255, 128, 0)
	if !strings.Contains(esc, "38;2;255;128;0") {
		t.Errorf("unexpected escape: %q", esc)
	}
}

func TestRGBBg(t *testing.T) {
	esc := colors.RGBBg(0, 0, 0)
	if !strings.Contains(esc, "48;2;0;0;0") {
		t.Errorf("unexpected escape: %q", esc)
	}
}

func TestHex(t *testing.T) {
	esc := colors.Hex("#ff8800")
	if !strings.Contains(esc, "38;2;") {
		t.Errorf("unexpected hex escape: %q", esc)
	}
}

func TestHexNoHash(t *testing.T) {
	esc := colors.Hex("00ff00")
	if !strings.Contains(esc, "38;2;") {
		t.Errorf("unexpected hex escape: %q", esc)
	}
}

func TestHexShort(t *testing.T) {
	esc := colors.Hex("#abc")
	// too short — should return ""
	if esc != "" {
		t.Errorf("expected empty for short hex, got %q", esc)
	}
}

func TestHexBg(t *testing.T) {
	esc := colors.HexBg("#ffffff")
	if !strings.Contains(esc, "48;2;") {
		t.Errorf("unexpected bg escape: %q", esc)
	}
}

func TestColor256(t *testing.T) {
	esc := colors.Color256(200)
	if !strings.Contains(esc, "38;5;200") {
		t.Errorf("got %q", esc)
	}
}

func TestColor256Bg(t *testing.T) {
	esc := colors.Color256Bg(50)
	if !strings.Contains(esc, "48;5;50") {
		t.Errorf("got %q", esc)
	}
}

// ---- Gradient ---------------------------------------------------------------

func TestGradientContainsText(t *testing.T) {
	result := colors.Gradient("hello", [3]int{255, 0, 0}, [3]int{0, 0, 255}, false, false)
	stripped := colors.StripANSI(result)
	if !strings.Contains(stripped, "hello") {
		t.Fatalf("Gradient missing text, stripped=%q", stripped)
	}
}

func TestGradientBold(t *testing.T) {
	result := colors.Gradient("ab", [3]int{0, 0, 0}, [3]int{255, 255, 255}, false, true)
	if !strings.Contains(result, colors.Bold) {
		t.Fatal("expected bold in gradient")
	}
}

func TestGradientBg(t *testing.T) {
	result := colors.Gradient("X", [3]int{0, 0, 0}, [3]int{255, 255, 255}, true, false)
	if !strings.Contains(result, "48;2;") {
		t.Fatal("expected background escape")
	}
}

func TestGradientNewline(t *testing.T) {
	result := colors.Gradient("a\nb", [3]int{0, 0, 0}, [3]int{255, 255, 255}, false, false)
	if !strings.Contains(result, "\n") {
		t.Fatal("newline should be preserved")
	}
}

func TestGradientMulticolor(t *testing.T) {
	stops := [][3]int{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}}
	r := colors.GradientMulticolor("hello world", stops, false, false)
	stripped := colors.StripANSI(r)
	if !strings.Contains(stripped, "hello world") {
		t.Fatalf("text missing, stripped=%q", stripped)
	}
}

func TestGradientMulticolorTooFewStops(t *testing.T) {
	r := colors.GradientMulticolor("hi", [][3]int{{255, 0, 0}}, false, false)
	if r != "hi" {
		t.Errorf("expected plain text, got %q", r)
	}
}

// ---- Preset gradients -------------------------------------------------------

func gradientContains(t *testing.T, result, text string) {
	t.Helper()
	if !strings.Contains(colors.StripANSI(result), text) {
		t.Fatalf("gradient missing %q, stripped=%q", text, colors.StripANSI(result))
	}
}

func TestFire(t *testing.T) {
	gradientContains(t, colors.Fire("fire", false), "fire")
}

func TestOcean(t *testing.T) {
	gradientContains(t, colors.Ocean("ocean", false), "ocean")
}

func TestForest(t *testing.T) {
	gradientContains(t, colors.Forest("forest", true), "forest")
}

func TestSunset(t *testing.T) {
	gradientContains(t, colors.Sunset("sunset", false), "sunset")
}

func TestAurora(t *testing.T) {
	gradientContains(t, colors.Aurora("aurora", false), "aurora")
}

func TestNeon(t *testing.T) {
	gradientContains(t, colors.Neon("neon", false), "neon")
}

func TestCandy(t *testing.T) {
	gradientContains(t, colors.Candy("candy", false), "candy")
}

func TestGoldGradient(t *testing.T) {
	gradientContains(t, colors.GoldGradient("gold", false), "gold")
}

func TestIce(t *testing.T) {
	gradientContains(t, colors.Ice("ice", false), "ice")
}

func TestLava(t *testing.T) {
	gradientContains(t, colors.Lava("lava", false), "lava")
}

func TestMatrix(t *testing.T) {
	gradientContains(t, colors.Matrix("matrix", false), "matrix")
}

func TestRose(t *testing.T) {
	gradientContains(t, colors.Rose("rose", false), "rose")
}

func TestRainbow(t *testing.T) {
	gradientContains(t, colors.Rainbow("rainbow", false), "rainbow")
}

// ---- Constants sanity -------------------------------------------------------

func TestConstantsNotEmpty(t *testing.T) {
	for name, code := range map[string]string{
		"Reset":     colors.Reset,
		"Bold":      colors.Bold,
		"Red":       colors.Red,
		"Green":     colors.Green,
		"BrightRed": colors.BrightRed,
		"BgBlue":    colors.BgBlue,
		"Orange":    colors.Orange,
	} {
		if code == "" {
			t.Errorf("constant %s is empty", name)
		}
	}
}
