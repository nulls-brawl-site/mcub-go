package pybridge

import (
	"testing"
)

func TestParseRequires_MCUB(t *testing.T) {
	src := `# requires: aiohttp Pillow pypdf
# meta name: TestMod
import aiohttp
`
	got := ParseRequires(src)
	want := []string{"aiohttp", "Pillow", "pypdf"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("[%d] got %q, want %q", i, got[i], w)
		}
	}
}

func TestParseRequires_Hikka_Comma(t *testing.T) {
	src := `# requires: aiohttp, Pillow, pypdf, python-docx`
	got := ParseRequires(src)
	if len(got) != 4 {
		t.Fatalf("got %v (len %d), want 4", got, len(got))
	}
}

func TestParseRequires_HikkaScope(t *testing.T) {
	src := `# scope: hikka_only
# scope: pip requests bs4
# meta name: X
`
	got := ParseRequires(src)
	if len(got) != 2 {
		t.Fatalf("got %v, want [requests bs4]", got)
	}
}

func TestParseRequires_MultiLine(t *testing.T) {
	src := `# requires: aiohttp
# requires: Pillow
# requires: pypdf python-docx
`
	got := ParseRequires(src)
	if len(got) != 4 {
		t.Fatalf("got %v (len %d), want 4", got, len(got))
	}
}

func TestParseRequires_Empty(t *testing.T) {
	src := `import os
import sys
class Mod: pass
`
	got := ParseRequires(src)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestParseRequires_NoDuplicates(t *testing.T) {
	src := `# requires: aiohttp aiohttp Pillow`
	got := ParseRequires(src)
	if len(got) != 2 {
		t.Fatalf("expected dedup to 2, got %v", got)
	}
}

func TestParseRequires_VersionSpec(t *testing.T) {
	// packages with version specifiers should be kept as-is for pip
	// but deduplicated by base name
	src := `# requires: aiohttp>=3.0 Pillow!=9.0`
	got := ParseRequires(src)
	if len(got) != 2 {
		t.Fatalf("got %v, want 2", got)
	}
	if got[0] != "aiohttp>=3.0" {
		t.Errorf("got %q, want aiohttp>=3.0", got[0])
	}
}

func TestResolvePipName(t *testing.T) {
	cases := map[string]string{
		"PIL":    "Pillow",
		"cv2":    "opencv-python",
		"yaml":   "PyYAML",
		"bs4":    "beautifulsoup4",
		"aiohttp": "aiohttp", // no mapping → unchanged
	}
	for imp, want := range cases {
		got := resolvePipName(imp)
		if got != want {
			t.Errorf("resolvePipName(%q) = %q, want %q", imp, got, want)
		}
	}
}

func TestIsNonInstallable(t *testing.T) {
	stdlib := []string{"os", "sys", "re", "json", "asyncio", "datetime"}
	for _, pkg := range stdlib {
		if !isNonInstallable(pkg) {
			t.Errorf("expected %q to be non-installable", pkg)
		}
	}
	installable := []string{"aiohttp", "Pillow", "requests", "telethon"}
	for _, pkg := range installable {
		if isNonInstallable(pkg) {
			t.Errorf("expected %q to be installable", pkg)
		}
	}
}

func TestParseRequires_QkDwMod(t *testing.T) {
	// real header from QkDw.py (PollenGen)
	src := `# requires: aiohttp Pillow pypdf python-docx openpyxl python-pptx
# scope: hikka_only
# meta name: PollenGen
`
	got := ParseRequires(src)
	want := []string{"aiohttp", "Pillow", "pypdf", "python-docx", "openpyxl", "python-pptx"}
	if len(got) != len(want) {
		t.Fatalf("got %v (len %d), want %v", got, len(got), want)
	}
}
