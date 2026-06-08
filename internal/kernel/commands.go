package kernel

import (
	"context"
	"fmt"

	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

const maxAliasDepth = 5

// ProcessCommand parses a NewMessage event and dispatches it to the registered
// command handler.  It detects pipeline expressions and delegates to
// ExecutePipeline when applicable.
//
// Flow:
//  1. Verify the message starts with the active prefix.
//  2. If the text contains pipeline operators, parse and execute as pipeline.
//  3. Otherwise resolve aliases and dispatch to the single handler.
func (k *Kernel) ProcessCommand(ctx context.Context, ev *events.NewMessage) error {
	text := ev.Text()

	// Pipeline detection: if the text has pipeline operators, execute as pipeline.
	if IsPipeline(text) {
		segs := ParsePipeline(text)
		if len(segs) > 1 {
			return k.ExecutePipeline(ctx, ev, segs)
		}
	}

	return k.dispatchSingleCommand(ctx, ev)
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

// AddAlias registers a new alias mapping from -> to and persists the config.
func (k *Kernel) AddAlias(from, to string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.Aliases[from] = to
	if k.Config != nil {
		if k.Config.Aliases == nil {
			k.Config.Aliases = make(map[string]string)
		}
		k.Config.Aliases[from] = to
		if k.ConfigFile != "" {
			return k.Config.Save(k.ConfigFile)
		}
	}
	return nil
}

// RemoveAlias removes the alias for the given command word and persists the config.
func (k *Kernel) RemoveAlias(alias string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.Aliases, alias)
	if k.Config != nil {
		delete(k.Config.Aliases, alias)
		if k.ConfigFile != "" {
			return k.Config.Save(k.ConfigFile)
		}
	}
	return nil
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
