// Package pybridge – pipeline.go
//
// PipelineCaptureState provides a way to pass pipeline execution context
// through a context.Context so that command handlers can receive pipe input
// and their output can be captured without modifying the public event API.
package pybridge

import (
	"context"
	"sync"
)

// PipelineCaptureState is created by the pipeline executor and attached to a
// context.Context before dispatching a piped command.  The command handler
// checks for it (via PipelineCaptureFromContext) and:
//   - sets BridgeEvent.PipeInput / BridgeEvent.IsPiped from the state, and
//   - wraps the EditFn to call Capture() so that the output is recorded.
type PipelineCaptureState struct {
	// Input is the text output of the preceding pipeline segment.
	Input string
	// IsPiped is true when this command runs as part of a pipeline.
	IsPiped bool

	mu     sync.Mutex
	output string
	hasOut bool
}

// Capture records text as the pipe output.  It is called by the wrapped
// EditFn each time event.edit() is invoked; the last call wins.
func (s *PipelineCaptureState) Capture(text string) {
	s.mu.Lock()
	s.output = text
	s.hasOut = true
	s.mu.Unlock()
}

// Output returns the captured pipe output and whether any output was set.
func (s *PipelineCaptureState) Output() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.output, s.hasOut
}

// ctxPipelineKey is the unexported context key for PipelineCaptureState.
type ctxPipelineKey struct{}

// WithPipelineCapture attaches state to ctx and returns the new context.
func WithPipelineCapture(ctx context.Context, state *PipelineCaptureState) context.Context {
	return context.WithValue(ctx, ctxPipelineKey{}, state)
}

// PipelineCaptureFromContext retrieves the PipelineCaptureState from ctx,
// returning nil when none is set.
func PipelineCaptureFromContext(ctx context.Context) *PipelineCaptureState {
	v, _ := ctx.Value(ctxPipelineKey{}).(*PipelineCaptureState)
	return v
}
