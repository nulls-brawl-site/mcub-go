// Package loader – hikka_compat.go
//
// Hikka/Heroku module compatibility detector.
// Provides helpers that classify a Python module's source code as written for
// the Hikka userbot framework, the Heroku userbot framework, the native MCUB
// API, or an unknown style.
package loader

import (
	"regexp"
	"strings"
)

// hikkaPatterns are strings that appear in Hikka userbot modules.
var hikkaPatterns = []string{
	"from hikka",
	"import hikka",
	"@loader.command",
	"loader.Module",
	"from .. import loader",
	"class.*loader.Module",
}

// herokuPatterns are strings that appear in Heroku userbot modules.
var herokuPatterns = []string{
	"from Heroku",
	"import Heroku",
	"from heroku",
	"Heroku.register_command",
}

// hikkaClassRe matches Hikka-style class declarations.
var hikkaClassRe = regexp.MustCompile(`(?m)class\s+\w+\s*\(\s*(?:\w+\.)*Module\s*\)`)

// IsHikkaModule returns true if the Python source code looks like a Hikka
// userbot module (uses hikka.loader, @loader.command decorators, etc.).
func IsHikkaModule(code string) bool {
	for _, p := range hikkaPatterns {
		if strings.Contains(code, p) {
			return true
		}
	}
	// Also detect class Foo(loader.Module) / class Foo(hikka.loader.Module)
	return hikkaClassRe.MatchString(code)
}

// IsHerokuModule returns true if the Python source code looks like a Heroku
// userbot module.
func IsHerokuModule(code string) bool {
	for _, p := range herokuPatterns {
		if strings.Contains(code, p) {
			return true
		}
	}
	return false
}

// DetectModuleFramework returns the framework the module was written for.
// Possible return values: "mcub", "hikka", "heroku", "unknown".
func DetectModuleFramework(code string) string {
	if IsHikkaModule(code) {
		return "hikka"
	}
	if IsHerokuModule(code) {
		return "heroku"
	}
	if strings.Contains(code, "ModuleBase") ||
		strings.Contains(code, "from core.lib") {
		return "mcub"
	}
	if strings.Contains(code, "def register(kernel)") {
		return "mcub"
	}
	return "unknown"
}
