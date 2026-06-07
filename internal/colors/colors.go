// Package colors provides ANSI terminal color codes and gradient utilities.
// Ported from core/lib/utils/colors.py.
// SPDX-License-Identifier: MIT
package colors

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Basic reset / style codes.
const (
	Reset      = "\033[0m"
	Bold       = "\033[1m"
	Dim        = "\033[2m"
	Italic     = "\033[3m"
	Underline  = "\033[4m"
	Blink      = "\033[5m"
	BlinkFast  = "\033[6m"
	Reverse    = "\033[7m"
	Hidden     = "\033[8m"
	Strikethrough    = "\033[9m"
	DoubleUnderline  = "\033[21m"
	Overline         = "\033[53m"

	ResetBold          = "\033[22m"
	ResetDim           = "\033[22m"
	ResetItalic        = "\033[23m"
	ResetUnderline     = "\033[24m"
	ResetBlink         = "\033[25m"
	ResetReverse       = "\033[27m"
	ResetHidden        = "\033[28m"
	ResetStrikethrough = "\033[29m"
	ResetFG            = "\033[39m"
	ResetBG            = "\033[49m"
)

// Standard foreground colors.
const (
	Black   = "\033[30m"
	Red     = "\033[31m"
	Green   = "\033[32m"
	Yellow  = "\033[33m"
	Blue    = "\033[34m"
	Purple  = "\033[35m"
	Cyan    = "\033[36m"
	White   = "\033[37m"
	Magenta = "\033[35m"
)

// Bright foreground colors.
const (
	BrightBlack   = "\033[90m" // dark grey
	BrightRed     = "\033[91m"
	BrightGreen   = "\033[92m"
	BrightYellow  = "\033[93m"
	BrightBlue    = "\033[94m"
	BrightPurple  = "\033[95m"
	BrightCyan    = "\033[96m"
	BrightWhite   = "\033[97m"
	BrightMagenta = "\033[95m"
)

// Background colors.
const (
	BgBlack   = "\033[40m"
	BgRed     = "\033[41m"
	BgGreen   = "\033[42m"
	BgYellow  = "\033[43m"
	BgBlue    = "\033[44m"
	BgPurple  = "\033[45m"
	BgCyan    = "\033[46m"
	BgWhite   = "\033[47m"
)

// Bright background colors.
const (
	BgBrightBlack  = "\033[100m"
	BgBrightRed    = "\033[101m"
	BgBrightGreen  = "\033[102m"
	BgBrightYellow = "\033[103m"
	BgBrightBlue   = "\033[104m"
	BgBrightPurple = "\033[105m"
	BgBrightCyan   = "\033[106m"
	BgBrightWhite  = "\033[107m"
)

// Semantic aliases.
const (
	Success = "\033[92m" // bright green
	Error   = "\033[91m" // bright red
	Warning = "\033[93m" // bright yellow
	Info    = "\033[96m" // bright cyan
	Debug   = "\033[95m" // bright purple
	Muted   = "\033[90m" // dark grey
	Grey    = "\033[90m"
	Gray    = "\033[90m"
	Pink    = "\033[95m"
)

// 256-color named constants.
const (
	Orange = "\033[38;5;214m"
	Lime   = "\033[38;5;154m"
	Teal   = "\033[38;5;30m"
	Maroon = "\033[38;5;88m"
	Navy   = "\033[38;5;17m"
	Gold   = "\033[38;5;220m"
	Violet = "\033[38;5;135m"
	Indigo = "\033[38;5;54m"
	Brown  = "\033[38;5;130m"
	Silver = "\033[38;5;250m"
)

// RGB returns a 24-bit true-color foreground escape for the given RGB values.
func RGB(r, g, b int) string {
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

// RGGBg returns a 24-bit true-color background escape for the given RGB values.
func RGBBg(r, g, b int) string {
	return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
}

// Color256 returns a 256-color foreground escape (0-255).
func Color256(n int) string {
	return fmt.Sprintf("\033[38;5;%dm", n)
}

// Color256Bg returns a 256-color background escape (0-255).
func Color256Bg(n int) string {
	return fmt.Sprintf("\033[48;5;%dm", n)
}

// Hex returns a 24-bit foreground escape from a hex color string like "#ff8800" or "ff8800".
func Hex(code string) string {
	code = strings.TrimPrefix(code, "#")
	if len(code) < 6 {
		return ""
	}
	var r, g, b int
	fmt.Sscanf(code[0:2], "%02x", &r)
	fmt.Sscanf(code[2:4], "%02x", &g)
	fmt.Sscanf(code[4:6], "%02x", &b)
	return RGB(r, g, b)
}

// HexBg returns a 24-bit background escape from a hex color string.
func HexBg(code string) string {
	code = strings.TrimPrefix(code, "#")
	if len(code) < 6 {
		return ""
	}
	var r, g, b int
	fmt.Sscanf(code[0:2], "%02x", &r)
	fmt.Sscanf(code[2:4], "%02x", &g)
	fmt.Sscanf(code[4:6], "%02x", &b)
	return RGBBg(r, g, b)
}

// Paint wraps text with one or more ANSI codes and appends Reset.
//
//	fmt.Println(Paint("hello", Bold, BrightGreen))
func Paint(text string, codes ...string) string {
	return strings.Join(codes, "") + text + Reset
}

// BoldText wraps text in bold.
func BoldText(text string) string {
	return Paint(text, Bold)
}

// ErrorText returns red bold text.
func ErrorText(text string) string {
	return Paint(text, Bold, BrightRed)
}

// SuccessText returns green bold text.
func SuccessText(text string) string {
	return Paint(text, Bold, BrightGreen)
}

// WarningText returns yellow text.
func WarningText(text string) string {
	return Paint(text, Yellow)
}

// InfoText returns cyan text.
func InfoText(text string) string {
	return Paint(text, Cyan)
}

// ansiRE matches all ANSI CSI escape sequences.
var ansiRE = regexp.MustCompile(`\033\[[0-9;]*m`)

// StripANSI removes all ANSI escape codes from text.
func StripANSI(text string) string {
	return ansiRE.ReplaceAllString(text, "")
}

// lerpRGB linearly interpolates between two RGB colours at t ∈ [0, 1].
func lerpRGB(r1, g1, b1, r2, g2, b2 int, t float64) (int, int, int) {
	lerp := func(a, b int) int {
		return int(math.Round(float64(a) + float64(b-a)*t))
	}
	return lerp(r1, r2), lerp(g1, g2), lerp(b1, b2)
}

// Gradient colours each character of text along a linear RGB gradient.
// If bg is true the gradient is applied to the background colour.
func Gradient(text string, start, end [3]int, bg, bold bool) string {
	chars := []rune(text)
	var visible []rune
	for _, c := range chars {
		if c != '\n' {
			visible = append(visible, c)
		}
	}
	n := len(visible) - 1
	if n < 1 {
		n = 1
	}
	style := ""
	if bold {
		style = Bold
	}
	var result strings.Builder
	idx := 0
	for _, ch := range chars {
		if ch == '\n' {
			result.WriteString(Reset + "\n")
			continue
		}
		t := float64(idx) / float64(n)
		r, g, b := lerpRGB(start[0], start[1], start[2], end[0], end[1], end[2], t)
		var esc string
		if bg {
			esc = RGBBg(r, g, b)
		} else {
			esc = RGB(r, g, b)
		}
		result.WriteString(style + esc + string(ch))
		idx++
	}
	result.WriteString(Reset)
	return result.String()
}

// GradientMulticolor colours each character through an arbitrary list of RGB
// colour stops (at least 2 required).
func GradientMulticolor(text string, stops [][3]int, bg, bold bool) string {
	if len(stops) < 2 {
		return text
	}
	chars := []rune(text)
	var visible []rune
	for _, c := range chars {
		if c != '\n' {
			visible = append(visible, c)
		}
	}
	n := len(visible) - 1
	if n < 1 {
		n = 1
	}
	segments := len(stops) - 1
	style := ""
	if bold {
		style = Bold
	}
	var result strings.Builder
	idx := 0
	for _, ch := range chars {
		if ch == '\n' {
			result.WriteString(Reset + "\n")
			continue
		}
		t := float64(idx) / float64(n)
		seg := int(t * float64(segments))
		if seg >= segments {
			seg = segments - 1
		}
		localT := t*float64(segments) - float64(seg)
		s1, s2 := stops[seg], stops[seg+1]
		r, g, b := lerpRGB(s1[0], s1[1], s1[2], s2[0], s2[1], s2[2], localT)
		var esc string
		if bg {
			esc = RGBBg(r, g, b)
		} else {
			esc = RGB(r, g, b)
		}
		result.WriteString(style + esc + string(ch))
		idx++
	}
	result.WriteString(Reset)
	return result.String()
}

// Named gradient presets.

// Fire applies a black → red → orange → yellow gradient.
func Fire(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{20, 0, 0}, {180, 0, 0}, {255, 120, 0}, {255, 220, 50}}, false, bold)
}

// Ocean applies a deep navy → teal → aqua → white-cyan gradient.
func Ocean(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{0, 10, 80}, {0, 80, 160}, {0, 180, 200}, {180, 240, 255}}, false, bold)
}

// Forest applies a dark green → lime → yellow-green gradient.
func Forest(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{0, 60, 10}, {0, 160, 40}, {80, 220, 30}, {200, 255, 80}}, false, bold)
}

// Sunset applies a purple → magenta → orange → yellow gradient.
func Sunset(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{80, 0, 120}, {200, 0, 160}, {255, 100, 20}, {255, 210, 80}}, false, bold)
}

// Aurora applies a green → cyan → purple → pink gradient.
func Aurora(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{0, 200, 100}, {0, 220, 200}, {100, 80, 220}, {220, 80, 180}}, false, bold)
}

// Neon applies a hot pink → electric blue gradient.
func Neon(text string, bold bool) string {
	return Gradient(text, [3]int{255, 0, 128}, [3]int{0, 128, 255}, false, bold)
}

// Candy applies a pink → lavender → baby blue gradient.
func Candy(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{255, 100, 180}, {200, 130, 255}, {100, 180, 255}}, false, bold)
}

// GoldGradient applies a dark gold → bright gold → white shimmer gradient.
func GoldGradient(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{120, 80, 0}, {220, 170, 0}, {255, 230, 100}, {255, 255, 200}}, false, bold)
}

// Ice applies a white → light blue → deep blue gradient.
func Ice(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{220, 240, 255}, {120, 200, 255}, {40, 120, 220}, {10, 40, 140}}, false, bold)
}

// Lava applies a dark red → bright red → yellow-white core gradient.
func Lava(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{60, 0, 0}, {200, 20, 0}, {255, 80, 0}, {255, 255, 180}}, false, bold)
}

// Matrix applies a black → dark green → bright green gradient.
func Matrix(text string, bold bool) string {
	return Gradient(text, [3]int{0, 20, 0}, [3]int{0, 255, 70}, false, bold)
}

// Rose applies a deep red → rose → blush pink gradient.
func Rose(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{{120, 0, 30}, {210, 30, 80}, {255, 130, 160}}, false, bold)
}

// Rainbow applies full spectrum: red → orange → yellow → green → blue → violet.
func Rainbow(text string, bold bool) string {
	return GradientMulticolor(text, [][3]int{
		{255, 0, 0},
		{255, 127, 0},
		{255, 255, 0},
		{0, 200, 0},
		{0, 0, 255},
		{139, 0, 255},
	}, false, bold)
}
