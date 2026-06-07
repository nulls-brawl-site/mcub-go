package scheduler_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nulls-brawl-site/mcub-go/internal/scheduler"
)

func TestAddRunStop(t *testing.T) {
	s := scheduler.New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ran := make(chan struct{}, 1)
	err := s.Add("test", 100*time.Millisecond, func(ctx context.Context) error {
		select {
		case ran <- struct{}{}:
		default:
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	s.Start(ctx)

	select {
	case <-ran:
		// task ran successfully
	case <-time.After(500 * time.Millisecond):
		t.Fatal("task did not run within 500ms")
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestAddDuplicateName(t *testing.T) {
	s := scheduler.New(nil)
	fn := func(ctx context.Context) error { return nil }

	if err := s.Add("task", 100*time.Millisecond, fn); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("task", 200*time.Millisecond, fn); err == nil {
		t.Fatal("expected error for duplicate task name")
	}
}

func TestAddZeroInterval(t *testing.T) {
	s := scheduler.New(nil)
	err := s.Add("zero", 0, func(ctx context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected error for zero interval")
	}
}

func TestRemove(t *testing.T) {
	s := scheduler.New(nil)
	s.Add("rm", 100*time.Millisecond, func(ctx context.Context) error { return nil })

	if err := s.Remove("rm"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Remove("rm"); err == nil {
		t.Fatal("expected error when removing non-existent task")
	}
}

func TestList(t *testing.T) {
	s := scheduler.New(nil)
	s.Add("t1", time.Second, func(ctx context.Context) error { return nil })
	s.Add("t2", time.Second, func(ctx context.Context) error { return nil })

	tasks := s.List()
	if len(tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(tasks))
	}
}

func TestStatus(t *testing.T) {
	s := scheduler.New(nil)
	s.Add("st", 100*time.Millisecond, func(ctx context.Context) error { return nil })

	task, ok := s.Status("st")
	if !ok {
		t.Fatal("expected task to be found")
	}
	if task.Name != "st" {
		t.Fatalf("expected name=st, got %q", task.Name)
	}

	_, ok2 := s.Status("nonexistent")
	if ok2 {
		t.Fatal("expected false for nonexistent task")
	}
}

func TestRunNow(t *testing.T) {
	s := scheduler.New(nil)
	var count int64

	s.Add("rn", 10*time.Second, func(ctx context.Context) error {
		atomic.AddInt64(&count, 1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx)

	if err := s.RunNow("rn"); err != nil {
		cancel()
		t.Fatalf("RunNow: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	cancel() // stop goroutines before s.Stop()
	s.Stop()

	if atomic.LoadInt64(&count) == 0 {
		t.Fatal("RunNow did not execute task")
	}
}

func TestRunNowNonExistent(t *testing.T) {
	s := scheduler.New(nil)
	if err := s.RunNow("ghost"); err == nil {
		t.Fatal("expected error for non-existent task")
	}
}

func TestErrorCounting(t *testing.T) {
	s := scheduler.New(nil)
	var runs int64

	s.Add("err_task", 50*time.Millisecond, func(ctx context.Context) error {
		atomic.AddInt64(&runs, 1)
		return errors.New("always fail")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	s.Start(ctx)
	<-ctx.Done()
	s.Stop()

	task, ok := s.Status("err_task")
	if !ok {
		t.Fatal("task not found")
	}
	if task.Errors == 0 && atomic.LoadInt64(&runs) > 0 {
		t.Fatal("expected error count > 0 after failures")
	}
}

func TestMultipleTasks(t *testing.T) {
	s := scheduler.New(nil)
	var a, b int64

	s.Add("a", 50*time.Millisecond, func(ctx context.Context) error {
		atomic.AddInt64(&a, 1)
		return nil
	})
	s.Add("b", 80*time.Millisecond, func(ctx context.Context) error {
		atomic.AddInt64(&b, 1)
		return nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	s.Start(ctx)
	<-ctx.Done()
	s.Stop()

	if atomic.LoadInt64(&a) == 0 {
		t.Fatal("task a did not run")
	}
	if atomic.LoadInt64(&b) == 0 {
		t.Fatal("task b did not run")
	}
}
