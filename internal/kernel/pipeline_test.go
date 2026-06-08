package kernel_test

import (
	"context"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// ---------------------------------------------------------------------------
// IsPipeline
// ---------------------------------------------------------------------------

func TestIsPipeline(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{".ping | .echo", true},
		{".ping && .echo", true},
		{".ping || .echo", true},
		{".ping & .echo", true},
		{".ping hello world", false},
		{".ping", false},
		{"", false},
		{"no prefix text", false},
		{".cmd1 | .cmd2 | .cmd3", true},
	}

	for _, c := range cases {
		got := kernel.IsPipeline(c.text)
		if got != c.want {
			t.Errorf("IsPipeline(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ParsePipeline
// ---------------------------------------------------------------------------

func TestParsePipelineSingleSegment(t *testing.T) {
	segs := kernel.ParsePipeline(".ping")
	if len(segs) != 1 {
		t.Fatalf("expected 1 segment, got %d", len(segs))
	}
	if segs[0].Command != ".ping" {
		t.Errorf("unexpected command: %q", segs[0].Command)
	}
	if segs[0].Operator != "" {
		t.Errorf("first segment should have empty operator, got %q", segs[0].Operator)
	}
}

func TestParsePipelineThreeSegments(t *testing.T) {
	segs := kernel.ParsePipeline(".ping | .echo hello | .info")
	if len(segs) != 3 {
		t.Fatalf("expected 3 segments, got %d: %+v", len(segs), segs)
	}
	if segs[0].Command != ".ping" {
		t.Errorf("seg[0].Command = %q, want .ping", segs[0].Command)
	}
	if segs[0].Operator != "" {
		t.Errorf("seg[0].Operator = %q, want empty", segs[0].Operator)
	}
	if segs[1].Command != ".echo hello" {
		t.Errorf("seg[1].Command = %q, want .echo hello", segs[1].Command)
	}
	if segs[1].Operator != "|" {
		t.Errorf("seg[1].Operator = %q, want |", segs[1].Operator)
	}
	if segs[2].Command != ".info" {
		t.Errorf("seg[2].Command = %q, want .info", segs[2].Command)
	}
	if segs[2].Operator != "|" {
		t.Errorf("seg[2].Operator = %q, want |", segs[2].Operator)
	}
}

func TestParsePipelineAndOperator(t *testing.T) {
	segs := kernel.ParsePipeline(".a && .b")
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[1].Operator != "&&" {
		t.Errorf("seg[1].Operator = %q, want &&", segs[1].Operator)
	}
}

func TestParsePipelineOrOperator(t *testing.T) {
	segs := kernel.ParsePipeline(".a || .b")
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[1].Operator != "||" {
		t.Errorf("seg[1].Operator = %q, want ||", segs[1].Operator)
	}
}

func TestParsePipelineBackgroundOperator(t *testing.T) {
	segs := kernel.ParsePipeline(".a & .b")
	if len(segs) != 2 {
		t.Fatalf("expected 2 segments, got %d", len(segs))
	}
	if segs[1].Operator != "&" {
		t.Errorf("seg[1].Operator = %q, want &", segs[1].Operator)
	}
}

func TestParsePipelineMixedOperators(t *testing.T) {
	segs := kernel.ParsePipeline(".a | .b && .c || .d")
	if len(segs) != 4 {
		t.Fatalf("expected 4 segments, got %d: %+v", len(segs), segs)
	}
	ops := []string{"", "|", "&&", "||"}
	for i, want := range ops {
		if segs[i].Operator != want {
			t.Errorf("seg[%d].Operator = %q, want %q", i, segs[i].Operator, want)
		}
	}
}

// ---------------------------------------------------------------------------
// ExecutePipeline (integration – no real Telegram client required)
// ---------------------------------------------------------------------------

func TestExecutePipelinePipe(t *testing.T) {
	k := makeTestKernel()

	// Register "ping" so the pipeline has something to dispatch.
	k.RegisterCommand("ping", "mod", "", func(_ context.Context, _ *events.NewMessage) error {
		return nil
	})

	segs := kernel.ParsePipeline(".ping | .ping")
	ev := makeEvent(".ping | .ping")
	if err := k.ExecutePipeline(context.Background(), ev, segs); err != nil {
		t.Fatalf("ExecutePipeline (pipe): unexpected error: %v", err)
	}
}

func TestExecutePipelineAnd(t *testing.T) {
	k := makeTestKernel()
	callCount := 0
	k.RegisterCommand("ok", "mod", "", func(_ context.Context, _ *events.NewMessage) error {
		callCount++
		return nil
	})

	segs := kernel.ParsePipeline(".ok && .ok")
	if err := k.ExecutePipeline(context.Background(), makeEvent(".ok && .ok"), segs); err != nil {
		t.Fatalf("ExecutePipeline (&&): %v", err)
	}
	// Both sides should have executed (first succeeds → second runs).
	if callCount != 2 {
		t.Fatalf("expected 2 calls, got %d", callCount)
	}
}

func TestExecutePipelineDepthLimit(t *testing.T) {
	k := makeTestKernel()

	// Build a pipeline with more segments than maxPipelineDepth via nesting.
	// We just verify no infinite loop / panic occurs.
	segs := []kernel.PipelineSegment{
		{Command: ".ping", Operator: ""},
		{Command: ".ping", Operator: "|"},
		{Command: ".ping", Operator: "|"},
		{Command: ".ping", Operator: "|"},
		{Command: ".ping", Operator: "|"},
		{Command: ".ping", Operator: "|"},
	}
	ev := makeEvent(".ping")
	_ = k.ExecutePipeline(context.Background(), ev, segs)
}
