package pybridge

// PyModule represents a loaded Python module together with the commands it
// registered via the @command decorator.
type PyModule struct {
	// Name is the canonical human-readable module name (from the class
	// attribute or derived from the file name).
	Name string

	// ModName is the Python sys.modules key used to find the module at call
	// time (typically the .py file stem).
	ModName string

	// Commands lists every command the module registered.
	Commands []PyCommand
}

// PyCommand describes a single command registered inside a Python module.
type PyCommand struct {
	// Name is the command trigger word (no prefix), e.g. "ping".
	Name string

	// Description is a short human-readable description (falls back to DocEN).
	Description string

	// DocRU is the Russian-language documentation string.
	DocRU string

	// DocEN is the English-language documentation string.
	DocEN string

	// ClassName is the name of the ModuleBase subclass that owns the method,
	// or an empty string for module-level functions.
	ClassName string

	// MethodName is the Python method/function name on the class or module.
	MethodName string
}
