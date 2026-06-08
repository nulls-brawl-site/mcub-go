// Package kernel – pipeline.go
//
// Implements pipeline parsing and execution for MCUB command strings.
// Supports the operators: | (pipe), && (and), || (or), & (background).
package kernel

import (
	"context"
	"fmt"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

const maxPipelineDepth = 5

// PipelineSegment represents one command in a pipeline.
type PipelineSegment struct {
	// Command is the full command text for this segment, including the prefix.
	Command string
	// Operator connects this segment to the previous one.
	// "" for the first segment, "|", "&&", "||", or "&" for the rest.
	Operator string
}

// IsPipeline returns true when text contains any pipeline operator.
func IsPipeline(text string) bool {
	for i := 0; i < len(text); i++ {
		if i+1 < len(text) {
			two := text[i : i+2]
			if two == "&&" || two == "||" {
				return true
			}
		}
		if text[i] == '|' || text[i] == '&' {
			return true
		}
	}
	return false
}

// ParsePipeline parses a pipeline expression into ordered segments.
//
// Example:
//
//	".ping | .echo foo && .info" →
//	  [{".ping",""}, {".echo foo","|"}, {".info","&&"}]
func ParsePipeline(text string) []PipelineSegment {
	var segments []PipelineSegment
	currentOp := ""
	start := 0
	i := 0

	for i < len(text) {
		// Two-character operators must be checked before single-char ones.
		if i+1 < len(text) {
			two := text[i : i+2]
			if two == "&&" || two == "||" {
				cmd := strings.TrimSpace(text[start:i])
				segments = append(segments, PipelineSegment{Command: cmd, Operator: currentOp})
				currentOp = two
				i += 2
				start = i
				continue
			}
		}
		switch text[i] {
		case '|':
			cmd := strings.TrimSpace(text[start:i])
			segments = append(segments, PipelineSegment{Command: cmd, Operator: currentOp})
			currentOp = "|"
			i++
			start = i
			continue
		case '&':
			cmd := strings.TrimSpace(text[start:i])
			segments = append(segments, PipelineSegment{Command: cmd, Operator: currentOp})
			currentOp = "&"
			i++
			start = i
			continue
		}
		i++
	}

	// Final segment.
	if start < len(text) {
		cmd := strings.TrimSpace(text[start:])
		if cmd != "" {
			segments = append(segments, PipelineSegment{Command: cmd, Operator: currentOp})
		}
	}
	return segments
}

// ExecutePipeline runs a multi-segment pipeline expression.
//
// Operator semantics:
//   - |   pipe the edit-output of the left side into pipe_input of the right side
//   - &&  execute the right side only when the left side succeeded (exitCode == 0)
//   - ||  execute the right side only when the left side failed  (exitCode != 0)
//   - &   execute the right side in the background (fire-and-forget)
func (k *Kernel) ExecutePipeline(ctx context.Context, ev *events.NewMessage, segments []PipelineSegment) error {
	return k.executePipelineDepth(ctx, ev, segments, 0)
}

func (k *Kernel) executePipelineDepth(ctx context.Context, ev *events.NewMessage, segments []PipelineSegment, depth int) error {
	if depth >= maxPipelineDepth {
		return fmt.Errorf("pipeline: depth limit exceeded (%d)", maxPipelineDepth)
	}
	if len(segments) == 0 {
		return nil
	}

	// Execute first segment (no operator, no pipe input).
	output, exitCode, _ := k.runAndCapture(ctx, ev, segments[0].Command, "", false)

	// Process remaining segments.
	for _, seg := range segments[1:] {
		switch seg.Operator {
		case "|":
			// Pipe: pass previous output as pipe_input to next command.
			output, exitCode, _ = k.runAndCapture(ctx, ev, seg.Command, output, true)

		case "&&":
			// And: run next only if previous succeeded.
			if exitCode == 0 {
				output, exitCode, _ = k.runAndCapture(ctx, ev, seg.Command, "", false)
			}

		case "||":
			// Or: run next only if previous failed.
			if exitCode != 0 {
				output, exitCode, _ = k.runAndCapture(ctx, ev, seg.Command, "", false)
			}

		case "&":
			// Background: fire and forget.
			bgCmd := seg.Command
			go func() {
				_, _, _ = k.runAndCapture(context.Background(), ev, bgCmd, "", false)
			}()
			// Background commands don't update exit code or output.
		}
	}
	_ = output
	_ = exitCode
	return nil
}

// runAndCapture executes a single command segment and returns its pipe output.
//
// pipeInput is the text from the preceding pipe segment (may be empty).
// isPiped indicates that this command is part of a pipe chain.
//
// The output is whatever the command's last event.edit() call produced.
// exitCode is 0 on success and 1 on error.
func (k *Kernel) runAndCapture(ctx context.Context, ev *events.NewMessage, cmdText string, pipeInput string, isPiped bool) (output string, exitCode int, err error) {
	capture := &pybridge.PipelineCaptureState{
		Input:   pipeInput,
		IsPiped: isPiped,
	}
	pipeCtx := pybridge.WithPipelineCapture(ctx, capture)

	// Create a shallow copy of ev with the pipeline segment text.
	modEv := cloneEventWithText(ev, cmdText)

	err = k.dispatchSingleCommand(pipeCtx, modEv)
	if err != nil {
		exitCode = 1
	}

	if out, ok := capture.Output(); ok {
		output = out
	}
	return output, exitCode, err
}

// cloneEventWithText returns a shallow copy of ev whose Text() returns text.
// The original Raw message is NOT mutated.
func cloneEventWithText(ev *events.NewMessage, text string) *events.NewMessage {
	if ev == nil {
		return nil
	}
	evCopy := *ev
	if ev.Raw != nil {
		rawCopy := *ev.Raw
		rawCopy.Message = text
		evCopy.Raw = &rawCopy
	} else {
		// Construct a minimal raw message.
		evCopy.Raw = &tg.Message{Message: text}
	}
	return &evCopy
}

// dispatchSingleCommand parses the command word from ev.Text() and dispatches
// it to the registered handler.  It is the inner function shared between
// ProcessCommand and the pipeline executor.
func (k *Kernel) dispatchSingleCommand(ctx context.Context, ev *events.NewMessage) error {
	text := ev.Text()
	prefix := k.Prefix()

	k.Log.Debug("dispatchSingleCommand: text=%q outgoing=%v senderID=%d adminID=%d prefix=%q",
		text, ev.IsOutgoing, ev.SenderID, k.AdminID, prefix)

	// Only process outgoing messages (sent by the authenticated account).
	if !ev.IsOutgoing {
		// Also accept messages from self in case IsOutgoing is not set
		// (some Telegram clients / DC configs don't set the Out flag).
		if k.AdminID == 0 || ev.SenderID != k.AdminID {
			return nil
		}
	}

	if !strings.HasPrefix(text, prefix) {
		return nil // not a command
	}

	body := strings.TrimPrefix(text, prefix)
	if body == "" {
		return nil
	}

	parts := strings.Fields(body)
	if len(parts) == 0 {
		return nil
	}

	cmdWord := strings.ToLower(parts[0])

	resolved, err := k.resolveAlias(cmdWord, maxAliasDepth)
	if err != nil {
		return err
	}

	k.mu.RLock()
	handler, found := k.CommandHandlers[resolved]
	k.mu.RUnlock()

	if !found {
		return nil // unknown command – silently ignore
	}

	return handler(ctx, ev)
}
