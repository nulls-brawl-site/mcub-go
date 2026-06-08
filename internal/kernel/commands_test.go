package kernel_test

import (
	"context"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/config"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

func makeTestKernel() *kernel.Kernel {
	cfg := &config.Config{
		CommandPrefix: ".",
		Aliases:       make(map[string]string),
	}
	return kernel.New(cfg, "test.json", kernel.KernelStandard)
}

func makeEvent(text string) *events.NewMessage {
	return &events.NewMessage{Raw: &tg.Message{Message: text}}
}

// ---------------------------------------------------------------------------

func TestProcessCommandNoPrefix(t *testing.T) {
	k := makeTestKernel()
	ev := makeEvent("hello world")
	// Should not return an error; non-prefixed messages are silently ignored.
	if err := k.ProcessCommand(context.Background(), ev); err != nil {
		t.Fatalf("ProcessCommand (no prefix): unexpected error: %v", err)
	}
}

func TestProcessCommandWithPrefix(t *testing.T) {
	k := makeTestKernel()
	called := false
	k.RegisterCommand("test", "mod", "test command", func(_ context.Context, _ *events.NewMessage) error {
		called = true
		return nil
	})
	ev := makeEvent(".test")
	if err := k.ProcessCommand(context.Background(), ev); err != nil {
		t.Fatalf("ProcessCommand: unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected handler to be called, but it was not")
	}
}

func TestProcessCommandUnknown(t *testing.T) {
	k := makeTestKernel()
	ev := makeEvent(".unknowncmd")
	// Unknown commands are silently ignored (no error, no handler called).
	if err := k.ProcessCommand(context.Background(), ev); err != nil {
		t.Fatalf("ProcessCommand (unknown): unexpected error: %v", err)
	}
}

func TestAliasResolution(t *testing.T) {
	k := makeTestKernel()
	called := false
	k.RegisterCommand("ping", "mod", "", func(_ context.Context, _ *events.NewMessage) error {
		called = true
		return nil
	})
	k.AddAlias("p", "ping")
	ev := makeEvent(".p")
	if err := k.ProcessCommand(context.Background(), ev); err != nil {
		t.Fatalf("ProcessCommand (alias): unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected aliased handler to be called, but it was not")
	}
}

func TestAliasChain(t *testing.T) {
	k := makeTestKernel()
	called := false
	k.RegisterCommand("target", "mod", "", func(_ context.Context, _ *events.NewMessage) error {
		called = true
		return nil
	})
	// Two-hop alias: x -> y -> target
	k.AddAlias("y", "target")
	k.AddAlias("x", "y")
	ev := makeEvent(".x")
	if err := k.ProcessCommand(context.Background(), ev); err != nil {
		t.Fatalf("ProcessCommand (alias chain): unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected chained alias handler to be called")
	}
}

func TestMaxAliasDepth(t *testing.T) {
	// Build a chain that exceeds the 5-level depth limit.
	k := makeTestKernel()
	k.AddAlias("a", "b")
	k.AddAlias("b", "c")
	k.AddAlias("c", "d")
	k.AddAlias("d", "e")
	k.AddAlias("e", "f")
	k.AddAlias("f", "g")

	ev := makeEvent(".a")
	// Must not panic or loop forever; an error is acceptable.
	// (The kernel returns an error when alias depth is exceeded.)
	_ = k.ProcessCommand(context.Background(), ev)
}

func TestRegisterAndUnregisterCommand(t *testing.T) {
	k := makeTestKernel()
	k.RegisterCommand("foo", "mod", "desc", func(_ context.Context, _ *events.NewMessage) error {
		return nil
	})
	cmds := k.ListCommands()
	found := false
	for _, c := range cmds {
		if c == "foo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("registered command not found in ListCommands()")
	}

	k.UnregisterCommand("foo")
	cmds = k.ListCommands()
	for _, c := range cmds {
		if c == "foo" {
			t.Fatal("unregistered command still appears in ListCommands()")
		}
	}
}

func TestHelpText(t *testing.T) {
	k := makeTestKernel()
	k.RegisterCommand("bar", "mymod", "bar description", func(_ context.Context, _ *events.NewMessage) error {
		return nil
	})
	help := k.HelpText("bar")
	if help == "" {
		t.Fatal("expected non-empty help text for registered command")
	}
}

func TestHelpTextUnknown(t *testing.T) {
	k := makeTestKernel()
	if got := k.HelpText("nonexistent"); got != "" {
		t.Fatalf("expected empty help text for unknown command, got %q", got)
	}
}

func TestRemoveAlias(t *testing.T) {
	k := makeTestKernel()
	called := false
	k.RegisterCommand("ping", "mod", "", func(_ context.Context, _ *events.NewMessage) error {
		called = true
		return nil
	})
	k.AddAlias("p", "ping")
	k.RemoveAlias("p")
	_ = k.ProcessCommand(context.Background(), makeEvent(".p"))
	if called {
		t.Fatal("handler should not be called after alias is removed")
	}
}
