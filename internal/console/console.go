// Package console ports core/console/shell.py — an interactive terminal shell
// that lets an operator control the running MCUB kernel without restarting it.
//
// The shell supports a small set of built-in commands plus any commands that
// the kernel exposes through the KernelOperator interface.
package console

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// KernelOperator — optional interface the kernel may implement so the console
// can call back into it.  All methods are optional; the console degrades
// gracefully when the kernel does not implement them.
// ─────────────────────────────────────────────────────────────────────────────

// KernelOperator is the interface the kernel may implement to allow the
// console to introspect and control it.
type KernelOperator interface {
	// ListModules returns the names of all currently loaded modules.
	ListModules() []string
	// ReloadModule reloads the named module.
	ReloadModule(name string) error
	// LoadModule loads a module from the given path.
	LoadModule(path string) error
	// UnloadModule unloads the named module.
	UnloadModule(name string) error
	// DispatchCommand dispatches a raw command string to the kernel's pipeline.
	DispatchCommand(cmd string) (string, error)
	// GetConfig returns the value of a kernel configuration key.
	GetConfig(key string) (string, bool)
	// SetConfig sets a kernel configuration key.
	SetConfig(key, value string) error
	// Status returns a human-readable status string.
	Status() string
	// Restart schedules a kernel restart.
	Restart() error
	// Stop initiates a graceful shutdown.
	Stop() error
	// RecentLog returns the most recent log lines.
	RecentLog(n int) []string
}

// ─────────────────────────────────────────────────────────────────────────────
// Console
// ─────────────────────────────────────────────────────────────────────────────

const defaultPrompt = "mcub❯ "

// Console provides an interactive terminal shell for controlling the kernel.
type Console struct {
	kernel  interface{}
	scanner *bufio.Scanner
	running bool
	prompt  string
	history []string
}

// New creates a new Console bound to the given kernel instance (may be nil in
// tests).
func New(kernel interface{}) *Console {
	return &Console{
		kernel:  kernel,
		scanner: bufio.NewScanner(os.Stdin),
		prompt:  defaultPrompt,
	}
}

// Run starts the interactive console loop, reading commands from stdin.
// It blocks until the user types "exit", the context is cancelled, or stdin
// reaches EOF.
func (c *Console) Run(ctx context.Context) error {
	c.running = true
	fmt.Printf("\n  MCUB Console  — type 'help' for commands\n\n")

	for c.running {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fmt.Print(c.prompt)
		if !c.scanner.Scan() {
			// EOF or error
			break
		}

		line := strings.TrimSpace(c.scanner.Text())
		if line == "" {
			continue
		}

		// Persist history (skip duplicates)
		if len(c.history) == 0 || c.history[len(c.history)-1] != line {
			c.history = append(c.history, line)
		}

		out, err := c.Execute(line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		} else if out != "" {
			fmt.Println(out)
		}
	}

	c.running = false
	return c.scanner.Err()
}

// Execute runs a single console command and returns the output string.
// It is safe to call Execute from outside the Run loop (e.g. for scripting).
func (c *Console) Execute(cmd string) (string, error) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return "", nil
	}
	name := strings.ToLower(parts[0])
	args := parts[1:]

	switch name {
	case "help":
		return c.cmdHelp(), nil

	case "modules":
		return c.cmdModules(), nil

	case "reload":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: reload <module>")
		}
		return c.cmdReload(args[0])

	case "load":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: load <path>")
		}
		return c.cmdLoad(args[0])

	case "unload":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: unload <module>")
		}
		return c.cmdUnload(args[0])

	case "cmd":
		if len(args) < 1 {
			return "", fmt.Errorf("usage: cmd <command>")
		}
		return c.cmdDispatch(strings.Join(args, " "))

	case "config":
		return c.cmdConfig(args)

	case "status":
		return c.cmdStatus(), nil

	case "restart":
		return c.cmdRestart()

	case "stop":
		return c.cmdStop()

	case "log":
		return c.cmdLog(args), nil

	case "clear":
		// Clear screen via ANSI escape
		return "\033[2J\033[H", nil

	case "history":
		return c.cmdHistory(), nil

	case "exit", "quit":
		c.running = false
		return "Goodbye.", nil

	default:
		return "", fmt.Errorf("unknown command %q — type 'help' for the list", name)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Command implementations
// ─────────────────────────────────────────────────────────────────────────────

func (c *Console) cmdHelp() string {
	return strings.Join([]string{
		"",
		"  Built-in commands:",
		"    help                   — show this list",
		"    modules                — list loaded modules",
		"    reload <name>          — reload module by name",
		"    load <path>            — load module from path",
		"    unload <name>          — unload module",
		"    cmd <command>          — dispatch a kernel command",
		"    config <key> [value]   — get or set a config key",
		"    status                 — kernel status",
		"    restart                — restart kernel",
		"    stop                   — stop kernel",
		"    log [n]                — show last n log lines (default 20)",
		"    history                — show command history",
		"    clear                  — clear screen",
		"    exit / quit            — leave the console",
		"",
	}, "\n")
}

func (c *Console) cmdModules() string {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "  (kernel does not expose module listing)"
	}
	names := ko.ListModules()
	if len(names) == 0 {
		return "  no modules loaded"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("  %d module(s) loaded:\n", len(names)))
	for _, n := range names {
		sb.WriteString("    • " + n + "\n")
	}
	return sb.String()
}

func (c *Console) cmdReload(name string) (string, error) {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "", fmt.Errorf("kernel does not support reload")
	}
	if err := ko.ReloadModule(name); err != nil {
		return "", err
	}
	return fmt.Sprintf("  module %q reloaded", name), nil
}

func (c *Console) cmdLoad(path string) (string, error) {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "", fmt.Errorf("kernel does not support load")
	}
	if err := ko.LoadModule(path); err != nil {
		return "", err
	}
	return fmt.Sprintf("  module loaded from %q", path), nil
}

func (c *Console) cmdUnload(name string) (string, error) {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "", fmt.Errorf("kernel does not support unload")
	}
	if err := ko.UnloadModule(name); err != nil {
		return "", err
	}
	return fmt.Sprintf("  module %q unloaded", name), nil
}

func (c *Console) cmdDispatch(cmd string) (string, error) {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "", fmt.Errorf("kernel does not support command dispatch")
	}
	return ko.DispatchCommand(cmd)
}

func (c *Console) cmdConfig(args []string) (string, error) {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "", fmt.Errorf("kernel does not expose config")
	}
	if len(args) == 0 {
		return "", fmt.Errorf("usage: config <key> [value]")
	}
	key := args[0]
	if len(args) == 1 {
		// get
		val, found := ko.GetConfig(key)
		if !found {
			return fmt.Sprintf("  %s = (not set)", key), nil
		}
		return fmt.Sprintf("  %s = %s", key, val), nil
	}
	// set
	value := strings.Join(args[1:], " ")
	if err := ko.SetConfig(key, value); err != nil {
		return "", err
	}
	return fmt.Sprintf("  %s = %s  (saved)", key, value), nil
}

func (c *Console) cmdStatus() string {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "  (kernel does not expose status)"
	}
	return "  " + ko.Status()
}

func (c *Console) cmdRestart() (string, error) {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "", fmt.Errorf("kernel does not support restart")
	}
	if err := ko.Restart(); err != nil {
		return "", err
	}
	return "  restart scheduled", nil
}

func (c *Console) cmdStop() (string, error) {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "", fmt.Errorf("kernel does not support stop")
	}
	if err := ko.Stop(); err != nil {
		return "", err
	}
	c.running = false
	return "  kernel stopping…", nil
}

func (c *Console) cmdLog(args []string) string {
	ko, ok := c.kernel.(KernelOperator)
	if !ok {
		return "  (kernel does not expose log)"
	}
	n := 20
	if len(args) > 0 {
		_, err := fmt.Sscanf(args[0], "%d", &n)
		if err != nil || n <= 0 {
			n = 20
		}
	}
	lines := ko.RecentLog(n)
	if len(lines) == 0 {
		return "  (no log entries)"
	}
	return strings.Join(lines, "\n")
}

func (c *Console) cmdHistory() string {
	if len(c.history) == 0 {
		return "  (no history)"
	}
	var sb strings.Builder
	for i, h := range c.history {
		sb.WriteString(fmt.Sprintf("  %4d  %s\n", i+1, h))
	}
	return sb.String()
}
