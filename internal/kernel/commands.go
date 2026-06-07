package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

const maxAliasDepth = 5

// ProcessCommand parses a NewMessage event and dispatches it to the registered
// command handler. It mirrors the Python process_command logic.
//
// Flow:
//  1. Verify the message starts with the active prefix.
//  2. Split the command word from optional arguments.
//  3. Resolve aliases (recursive, capped at maxAliasDepth).
//  4. Look up and invoke the handler.
func (k *Kernel) ProcessCommand(ctx context.Context, ev *events.NewMessage) error {
	text := ev.Text()
	prefix := k.Prefix()

	if !strings.HasPrefix(text, prefix) {
		return nil // Not a command.
	}

	// Strip prefix and split into command + args.
	body := strings.TrimPrefix(text, prefix)
	if body == "" {
		return nil
	}

	parts := strings.Fields(body)
	if len(parts) == 0 {
		return nil
	}

	cmdWord := strings.ToLower(parts[0])

	// Resolve aliases up to maxAliasDepth levels deep.
	resolved, err := k.resolveAlias(cmdWord, maxAliasDepth)
	if err != nil {
		return err
	}

	k.mu.RLock()
	handler, found := k.CommandHandlers[resolved]
	k.mu.RUnlock()

	if !found {
		// Unknown command – silently ignore (matches Python behaviour).
		return nil
	}

	return handler(ctx, ev)
}

// resolveAlias recursively follows alias chains.
// Returns an error if the depth limit is exceeded (cycle guard).
func (k *Kernel) resolveAlias(cmd string, depth int) (string, error) {
	if depth <= 0 {
		return "", fmt.Errorf("alias depth limit exceeded for command %q (possible cycle)", cmd)
	}

	k.mu.RLock()
	target, isAlias := k.Aliases[cmd]
	k.mu.RUnlock()

	if !isAlias {
		return cmd, nil
	}

	// Aliases can themselves be aliased.
	return k.resolveAlias(target, depth-1)
}

// AddAlias registers a new alias mapping from -> to.
// Aliases are persisted in the Config on the next Save.
func (k *Kernel) AddAlias(from, to string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.Aliases[from] = to
	if k.Config != nil {
		k.Config.Aliases[from] = to
	}
}

// RemoveAlias removes the alias for the given command word.
func (k *Kernel) RemoveAlias(alias string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.Aliases, alias)
	if k.Config != nil {
		delete(k.Config.Aliases, alias)
	}
}

// ListCommands returns a snapshot of all registered command names.
func (k *Kernel) ListCommands() []string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	names := make([]string, 0, len(k.CommandHandlers))
	for name := range k.CommandHandlers {
		names = append(names, name)
	}
	return names
}

// HelpText returns the help string for a given command, or an empty string
// if the command is unknown.
func (k *Kernel) HelpText(cmd string) string {
	k.mu.RLock()
	defer k.mu.RUnlock()
	doc, ok := k.CommandDocs[cmd]
	if !ok {
		return ""
	}
	return fmt.Sprintf("%s – %s (module: %s)", cmd, doc.Description, doc.ModuleName)
}
