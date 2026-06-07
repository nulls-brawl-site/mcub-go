package kernel

import (
	"context"

	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// OnNewMessage is the entry-point handler wired to the Telegram client.
// It runs all kernel-level middlewares before dispatching to ProcessCommand.
func (k *Kernel) OnNewMessage(ctx context.Context, ev *events.NewMessage) error {
	// Build the final handler (ProcessCommand) wrapped in middleware.
	final := events.Handler(func(ctx context.Context, e events.Event) error {
		nm, ok := e.(*events.NewMessage)
		if !ok {
			return nil
		}
		return k.ProcessCommand(ctx, nm)
	})

	// Wrap with kernel middlewares (last registered = outermost).
	middlewares := k.Middlewares()
	for i := len(middlewares) - 1; i >= 0; i-- {
		mw := middlewares[i]
		next := final
		final = func(ctx context.Context, e events.Event) error {
			return mw(func(ctx2 context.Context, e2 events.Event) error {
				return next(ctx2, e2)
			})(ctx, e)
		}
	}

	return final(ctx, ev)
}

// RegisterHandlers wires the kernel's event handlers into the MCUBClient.
// Call this after the client has been created.
func (k *Kernel) RegisterHandlers() {
	if k.Client == nil {
		return
	}

	// Register the main new-message handler.
	k.Client.AddEventHandler(
		events.NewMessageFilter(),
		func(ctx context.Context, e events.Event) error {
			nm, ok := e.(*events.NewMessage)
			if !ok {
				return nil
			}
			return k.OnNewMessage(ctx, nm)
		},
	)
}

// LoggingMiddleware returns a Middleware that logs every command invocation.
func LoggingMiddleware(log interface{ Info(string, ...interface{}) }) Middleware {
	return func(next events.Handler) events.Handler {
		return func(ctx context.Context, e events.Event) error {
			if nm, ok := e.(*events.NewMessage); ok {
				log.Info("event: NewMessage peer=%d text=%q", nm.PeerID, nm.Text())
			}
			return next(ctx, e)
		}
	}
}
