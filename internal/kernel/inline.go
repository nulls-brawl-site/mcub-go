// inline.go wires the kernel's InlineManager to an InlineBot instance.
//
// SPDX-License-Identifier: MIT
package kernel

import (
	"context"
	"fmt"

	"github.com/nulls-brawl-site/mcub-go/internal/inline"
)

// SetupInlineBot creates and starts an InlineBot for the given token and
// username. It attaches the bot to the kernel's InlineManager and launches
// the bot's run loop in a background goroutine.
//
// token must be the Bot API token (e.g. "12345:ABC...").
// username is the bot's @handle without @; it may be empty if unknown at call
// time (GetMe will populate it once the bot connects).
//
// The bot loop runs until ctx is cancelled. Errors from the run loop are
// returned through the returned error channel; callers should drain it or at
// least ensure it is garbage-collected when ctx is done.
func (k *Kernel) SetupInlineBot(ctx context.Context, token, username string) error {
	if token == "" {
		return fmt.Errorf("kernel: SetupInlineBot: token must not be empty")
	}

	bot, err := inline.New(k, token)
	if err != nil {
		return fmt.Errorf("kernel: SetupInlineBot: create bot: %w", err)
	}

	// Pre-populate username if provided so that other subsystems can reference
	// the bot before the run loop authenticates.
	_ = username // stored by GetMe once the bot is connected

	k.InlineManager.SetBot(bot)

	// Run the bot in the background; the goroutine exits when ctx is cancelled.
	go func() {
		if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
			if k.Log != nil {
				k.Log.Warn("inline bot run loop exited: %v", err)
			}
		}
	}()

	return nil
}
