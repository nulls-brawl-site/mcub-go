// Package scheduler provides a named periodic task scheduler.
// Ported from core/lib/time/scheduler.py (TaskScheduler).
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Logger is the minimal interface the scheduler needs for logging.
type Logger interface {
	Debug(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}

// nopLogger silently discards log messages when no real logger is provided.
type nopLogger struct{}

func (nopLogger) Debug(string, ...interface{}) {}
func (nopLogger) Error(string, ...interface{}) {}

// Task represents a single registered periodic task.
type Task struct {
	Name     string
	Interval time.Duration
	Fn       func(ctx context.Context) error

	mu      sync.Mutex
	LastRun time.Time
	NextRun time.Time
	Running bool
	Errors  int
}

// TaskScheduler manages a collection of named periodic tasks.
type TaskScheduler struct {
	mu     sync.RWMutex
	tasks  map[string]*Task
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	log    Logger
}

// New creates a TaskScheduler. log may be any value that implements Logger; if
// it does not (or is nil) a no-op logger is used instead.
func New(log interface{}) *TaskScheduler {
	var l Logger
	if typed, ok := log.(Logger); ok {
		l = typed
	} else {
		l = nopLogger{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &TaskScheduler{
		tasks:  make(map[string]*Task),
		ctx:    ctx,
		cancel: cancel,
		log:    l,
	}
}

// Add registers a task to run at interval. Returns an error if the name is
// already registered or interval is zero.
func (s *TaskScheduler) Add(name string, interval time.Duration, fn func(ctx context.Context) error) error {
	if interval <= 0 {
		return fmt.Errorf("scheduler: interval must be > 0 for task %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[name]; exists {
		return fmt.Errorf("scheduler: task %q already registered", name)
	}
	s.tasks[name] = &Task{
		Name:     name,
		Interval: interval,
		Fn:       fn,
		NextRun:  time.Now().Add(interval),
	}
	s.log.Debug("[Scheduler] task added name=%s interval=%s", name, interval)
	return nil
}

// Remove cancels and removes a task by name. Returns an error if not found.
func (s *TaskScheduler) Remove(name string) error {
	s.mu.Lock()
	_, ok := s.tasks[name]
	if ok {
		delete(s.tasks, name)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("scheduler: task %q not found", name)
	}
	s.log.Debug("[Scheduler] task removed name=%s", name)
	return nil
}

// Start begins executing all registered tasks using the provided context.
// If ctx is nil, the scheduler's own internal context is used.
// Start is idempotent; calling it again after Stop restarts the scheduler.
func (s *TaskScheduler) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = s.ctx
	}
	s.log.Debug("[Scheduler] start")

	s.mu.RLock()
	names := make([]string, 0, len(s.tasks))
	for n := range s.tasks {
		names = append(names, n)
	}
	s.mu.RUnlock()

	for _, name := range names {
		s.launchTask(ctx, name)
	}
	return nil
}

// launchTask spawns the background goroutine for a single task.
func (s *TaskScheduler) launchTask(ctx context.Context, name string) {
	s.mu.RLock()
	task, ok := s.tasks[name]
	s.mu.RUnlock()
	if !ok {
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.log.Debug("[Scheduler] goroutine started name=%s", task.Name)
		for {
			// Wait until NextRun or context cancellation.
			task.mu.Lock()
			delay := time.Until(task.NextRun)
			task.mu.Unlock()

			if delay < 0 {
				delay = 0
			}

			select {
			case <-ctx.Done():
				s.log.Debug("[Scheduler] goroutine stopped name=%s", task.Name)
				return
			case <-time.After(delay):
			}

			// Check task still registered.
			s.mu.RLock()
			_, exists := s.tasks[task.Name]
			s.mu.RUnlock()
			if !exists {
				return
			}

			s.runTask(ctx, task)
		}
	}()
}

// runTask executes task.Fn once, updating metadata.
func (s *TaskScheduler) runTask(ctx context.Context, task *Task) {
	task.mu.Lock()
	if task.Running {
		task.mu.Unlock()
		return
	}
	task.Running = true
	task.mu.Unlock()

	defer func() {
		task.mu.Lock()
		task.Running = false
		task.LastRun = time.Now()
		task.NextRun = task.LastRun.Add(task.Interval)
		task.mu.Unlock()
	}()

	if err := task.Fn(ctx); err != nil {
		task.mu.Lock()
		task.Errors++
		task.mu.Unlock()
		s.log.Error("[Scheduler] task error name=%s err=%v", task.Name, err)
	}
}

// Stop signals all task goroutines to stop and waits for them to finish.
func (s *TaskScheduler) Stop() error {
	s.log.Debug("[Scheduler] stop")
	s.cancel()
	s.wg.Wait()
	// Reset context so the scheduler can be restarted.
	s.mu.Lock()
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.mu.Unlock()
	s.log.Debug("[Scheduler] stopped")
	return nil
}

// RunNow immediately runs a task in a new goroutine, ignoring its interval.
func (s *TaskScheduler) RunNow(name string) error {
	s.mu.RLock()
	task, ok := s.tasks[name]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("scheduler: task %q not found", name)
	}
	go s.runTask(s.ctx, task)
	return nil
}

// List returns a snapshot of all registered tasks.
func (s *TaskScheduler) List() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		cp := *t
		out = append(out, &cp)
	}
	return out
}

// Status returns the Task for name. The second return is false if not found.
func (s *TaskScheduler) Status(name string) (*Task, bool) {
	s.mu.RLock()
	t, ok := s.tasks[name]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}
