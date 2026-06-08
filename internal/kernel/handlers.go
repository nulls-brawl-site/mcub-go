package kernel

import (
	"context"
	"reflect"
	"time"

	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// ---- Type definitions -------------------------------------------------------

// HandlerFunc is a generic event handler (used in ProcessWithMiddleware).
type HandlerFunc func(ctx context.Context, ev interface{}) error

// MiddlewareFunc is a generic middleware that can intercept any event.
// It mirrors the Python KernelHandlersMixin.middleware_chain entries.
type MiddlewareFunc func(ctx context.Context, ev interface{}, next HandlerFunc) error

// RequestMiddlewareFunc is a transport-level middleware that wraps outgoing
// Telegram requests (analogous to Telethon's request middleware).
type RequestMiddlewareFunc func(req interface{}, next func(interface{}) error) error

// InlineCommand holds metadata for a single inline command exposed by a module.
type InlineCommand struct {
	Name        string
	Description string
	Module      string
}

// CommandInfo bundles the handler function, owning module name, and
// documentation for a single command.
type CommandInfo struct {
	Handler     CommandHandler
	Owner       string
	Description string
	Usage       string
}

// ---- Core dispatch ----------------------------------------------------------

// OnNewMessage is the entry-point handler wired to the Telegram client.
// It runs all kernel-level middlewares before dispatching to ProcessCommand.
func (k *Kernel) OnNewMessage(ctx context.Context, ev *events.NewMessage) error {
	k.Log.Debug("OnNewMessage: text=%q outgoing=%v senderID=%d", ev.Text(), ev.IsOutgoing, ev.SenderID)

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

// ---- EnsureCoreHandlers / EnsureModuleHandlers ------------------------------

// coreHandlerKey is used to mark the core handler so EnsureCoreHandlers can
// detect whether it needs to be re-registered.
var coreHandlerInstalled bool
var coreHandlerLastCall time.Time

// EnsureCoreHandlers re-registers the core message handler if the client is
// connected and the handler is absent. Debounced to 1-second resolution.
// Mirrors Python KernelHandlersMixin.ensure_core_message_handlers().
func (k *Kernel) EnsureCoreHandlers() error {
	k.mu.Lock()
	now := time.Now()
	if now.Sub(coreHandlerLastCall) < time.Second {
		k.mu.Unlock()
		return nil
	}
	coreHandlerLastCall = now
	k.mu.Unlock()

	if k.Client == nil {
		return nil
	}
	// In the Go client there is no way to introspect registered handlers
	// directly. We re-register unconditionally; AddEventHandler is idempotent
	// when the same function pointer is used (MCUBClient must handle that).
	k.RegisterHandlers()
	if k.Log != nil {
		k.Log.Debug("EnsureCoreHandlers: handlers re-registered")
	}
	return nil
}

// EnsureModuleHandlers re-registers event handlers for all loaded modules that
// expose a RegisterHandlers(client) method or equivalent. This is called after
// a module reload to restore any handlers that were lost.
// Mirrors Python KernelHandlersMixin.ensure_registered_module_handlers().
func (k *Kernel) EnsureModuleHandlers() error {
	if k.Client == nil {
		return nil
	}
	k.mu.RLock()
	// Combine system and loaded modules into one iteration.
	all := make(map[string]Module, len(k.LoadedModules)+len(k.SystemModules))
	for n, m := range k.SystemModules {
		all[n] = m
	}
	for n, m := range k.LoadedModules {
		all[n] = m
	}
	k.mu.RUnlock()

	for name, mod := range all {
		// Modules may optionally implement an EnsureHandlers() method.
		type handlersEnsurer interface {
			EnsureHandlers(kernel interface{}) error
		}
		if he, ok := mod.(handlersEnsurer); ok {
			if err := he.EnsureHandlers(k); err != nil && k.Log != nil {
				k.Log.Warn("EnsureModuleHandlers: module %s: %v", name, err)
			}
		}
	}
	return nil
}

// DedupeEventHandlers removes duplicate handler registrations that were added
// more than once (e.g. after a hot-reload). Returns the list of removed
// handler descriptions.
//
// In the Go MCUBClient duplicate handlers must be tracked by the kernel itself.
// This implementation walks the inlineHandlers and callbackHandlers maps and
// removes any key that has been registered under more than one alias pointing
// to the same underlying function value.
// Mirrors Python KernelHandlersMixin.dedupe_event_builders().
func (k *Kernel) DedupeEventHandlers() []string {
	k.mu.Lock()
	defer k.mu.Unlock()

	var removed []string

	// Dedupe inline handlers by function pointer.
	seen := make(map[uintptr]string)
	for pattern, handler := range k.inlineHandlers {
		fp := reflect.ValueOf(handler).Pointer()
		if prev, dup := seen[fp]; dup {
			delete(k.inlineHandlers, pattern)
			removed = append(removed, "inline:"+pattern+"(dup of "+prev+")")
		} else {
			seen[fp] = pattern
		}
	}

	// Dedupe callback handlers by function pointer.
	seen2 := make(map[uintptr]string)
	for pattern, handler := range k.callbackHandlers {
		fp := reflect.ValueOf(handler).Pointer()
		if prev, dup := seen2[fp]; dup {
			delete(k.callbackHandlers, pattern)
			removed = append(removed, "callback:"+pattern+"(dup of "+prev+")")
		} else {
			seen2[fp] = pattern
		}
	}

	return removed
}

// ---- Inline / callback handler registration ---------------------------------

// RegisterInlineHandler registers an inline query handler for the given pattern.
// Mirrors Python KernelHandlersMixin.register_inline_handler().
func (k *Kernel) RegisterInlineHandler(pattern string, handler interface{}) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.inlineHandlers[pattern] = handler
}

// UnregisterModuleInlineHandlers removes all inline handlers that were
// registered by the named module.
// Mirrors Python KernelHandlersMixin.unregister_module_inline_handlers().
func (k *Kernel) UnregisterModuleInlineHandlers(moduleName string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for pattern, owner := range k.inlineHandlerOwners {
		if owner == moduleName {
			delete(k.inlineHandlers, pattern)
			delete(k.inlineHandlerOwners, pattern)
		}
	}
}

// RegisterCallbackHandler registers a callback query handler for pattern.
// Mirrors Python KernelHandlersMixin.register_callback_handler().
func (k *Kernel) RegisterCallbackHandler(pattern string, handler interface{}) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.callbackHandlers[pattern] = handler
}

// GetModuleInlineCommands returns all InlineCommand entries registered by the
// named module.
// Mirrors Python KernelHandlersMixin.get_module_inline_commands().
func (k *Kernel) GetModuleInlineCommands(moduleName string) []InlineCommand {
	k.mu.RLock()
	defer k.mu.RUnlock()
	var out []InlineCommand
	for pattern, owner := range k.inlineHandlerOwners {
		if owner == moduleName {
			out = append(out, InlineCommand{
				Name:   pattern,
				Module: moduleName,
			})
		}
	}
	return out
}

// ---- Middleware management --------------------------------------------------

// AddEventMiddleware appends fn to the generic middleware chain and returns fn.
// Mirrors Python KernelHandlersMixin.add_event_middleware().
func (k *Kernel) AddEventMiddleware(fn MiddlewareFunc) MiddlewareFunc {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, existing := range k.middlewareChain {
		if sameFunc(existing, fn) {
			return fn // already registered
		}
	}
	k.middlewareChain = append(k.middlewareChain, fn)
	return fn
}

// RemoveEventMiddleware removes fn from the generic middleware chain.
// Mirrors Python KernelHandlersMixin.remove_event_middleware().
func (k *Kernel) RemoveEventMiddleware(fn MiddlewareFunc) {
	k.mu.Lock()
	defer k.mu.Unlock()
	chain := k.middlewareChain[:0]
	for _, mw := range k.middlewareChain {
		if !sameFunc(mw, fn) {
			chain = append(chain, mw)
		}
	}
	k.middlewareChain = chain
}

// AddRequestMiddleware appends fn to the request-level middleware chain.
// Mirrors Python KernelHandlersMixin.add_request_middleware().
func (k *Kernel) AddRequestMiddleware(fn RequestMiddlewareFunc) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, existing := range k.requestMiddlewareChain {
		if sameFunc(existing, fn) {
			return
		}
	}
	k.requestMiddlewareChain = append(k.requestMiddlewareChain, fn)
}

// RemoveRequestMiddleware removes fn from the request-level middleware chain.
func (k *Kernel) RemoveRequestMiddleware(fn RequestMiddlewareFunc) {
	k.mu.Lock()
	defer k.mu.Unlock()
	chain := k.requestMiddlewareChain[:0]
	for _, mw := range k.requestMiddlewareChain {
		if !sameFunc(mw, fn) {
			chain = append(chain, mw)
		}
	}
	k.requestMiddlewareChain = chain
}

// ProcessWithMiddleware runs ev through the generic middleware chain and then
// calls handler. Mirrors Python KernelHandlersMixin.process_with_middleware().
func (k *Kernel) ProcessWithMiddleware(ctx context.Context, ev interface{}, handler HandlerFunc) error {
	k.mu.RLock()
	chain := make([]MiddlewareFunc, len(k.middlewareChain))
	copy(chain, k.middlewareChain)
	k.mu.RUnlock()

	// Build the composed handler (last appended = outermost wrapper).
	final := handler
	for i := len(chain) - 1; i >= 0; i-- {
		mw := chain[i]
		next := final
		final = func(ctx2 context.Context, ev2 interface{}) error {
			return mw(ctx2, ev2, next)
		}
	}
	return final(ctx, ev)
}

// ---- GetCommand -------------------------------------------------------------

// GetCommand returns the handler, owning module and documentation for cmd.
// Mirrors Python KernelHandlersMixin.get_command().
func (k *Kernel) GetCommand(name string) CommandInfo {
	k.mu.RLock()
	defer k.mu.RUnlock()
	handler := k.CommandHandlers[name]
	owner := k.CommandOwners[name]
	doc := k.CommandDocs[name]
	return CommandInfo{
		Handler:     handler,
		Owner:       owner,
		Description: doc.Description,
		Usage:       doc.Usage,
	}
}

// ---- LoggingMiddleware ------------------------------------------------------

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

// ---- helpers ----------------------------------------------------------------

// sameFunc compares two function values by their pointer representation.
func sameFunc(a, b interface{}) bool {
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	if va.Kind() != reflect.Func || vb.Kind() != reflect.Func {
		return false
	}
	return va.Pointer() == vb.Pointer()
}
