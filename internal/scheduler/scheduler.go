package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// TaskFunc is the work performed when a scheduled task fires. The context is
// cancelled when the scheduler stops; long-running work should respect it.
type TaskFunc func(ctx context.Context)

// nowFunc is overridable for tests. Don't read time.Now directly outside this.
type nowFunc func() time.Time

type registered struct {
	id       string
	schedule Schedule
	fn       TaskFunc
	running  atomic.Bool // prevents overlapping runs of the same task
}

// Scheduler fires registered tasks at their cron-defined times. One internal
// goroutine ticks at 1-minute resolution aligned to the next wall-clock minute.
// Tasks are invoked in their own goroutines; the scheduler does not wait.
//
// A task that is still running when its next fire time arrives is skipped
// (logged). This matches TrueNAS replication behaviour and avoids stacking up
// long-running transfers.
//
// The zero value is not usable — call New.
type Scheduler struct {
	mu    sync.RWMutex
	tasks map[string]*registered

	now  nowFunc
	stop chan struct{}
	done chan struct{}
}

// New returns a Scheduler that has not yet been started. Call Start to begin
// firing tasks.
func New() *Scheduler {
	return &Scheduler{
		tasks: make(map[string]*registered),
		now:   func() time.Time { return time.Now() },
	}
}

// Register adds or replaces a task. Calling Register with an id that already
// exists overwrites the prior schedule and function. Safe to call from any
// goroutine; it does not need the scheduler to be started.
func (s *Scheduler) Register(id string, schedule Schedule, fn TaskFunc) {
	if id == "" || fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[id] = &registered{id: id, schedule: schedule, fn: fn}
}

// Unregister removes a task. A no-op if id is unknown. If the task is
// currently running, it continues to completion; future fires are skipped.
func (s *Scheduler) Unregister(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tasks, id)
}

// Start begins the tick loop. Tick boundaries are aligned to the next whole
// minute, so all tasks scheduled at "minute=N" fire together. Returns
// immediately. The first tick will land at the start of the next minute.
//
// Calling Start twice has no effect after the first call.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.stop != nil {
		s.mu.Unlock()
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.mu.Unlock()

	// Capture stop/done as locals so the run goroutine never races with Stop
	// nilling the struct fields. A nil channel in a select blocks forever, so
	// without this the goroutine could deadlock instead of exiting on close.
	go s.run(ctx, s.stop, s.done)
}

// Stop halts the tick loop and blocks until the loop goroutine exits.
// In-flight task goroutines are not awaited.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	stop, done := s.stop, s.done
	s.stop, s.done = nil, nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

func (s *Scheduler) run(ctx context.Context, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)

	// Wait until the start of the next wall-clock minute so subsequent ticks
	// land cleanly at :00 seconds.
	now := s.now()
	next := now.Truncate(time.Minute).Add(time.Minute)
	timer := time.NewTimer(next.Sub(now))
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-timer.C:
		}

		s.tick(ctx, s.now())

		now = s.now()
		next = now.Truncate(time.Minute).Add(time.Minute)
		timer.Reset(next.Sub(now))
	}
}

// tick fires every task whose schedule matches now and that isn't already
// running. Exported only so tests can drive the scheduler synchronously.
func (s *Scheduler) tick(ctx context.Context, now time.Time) {
	s.mu.RLock()
	due := make([]*registered, 0, len(s.tasks))
	for _, t := range s.tasks {
		if t.schedule.Matches(now) {
			due = append(due, t)
		}
	}
	s.mu.RUnlock()

	for _, t := range due {
		if !t.running.CompareAndSwap(false, true) {
			slog.Info("scheduler: skipping fire, previous run still in progress",
				"task", t.id, "schedule", t.schedule.String())
			continue
		}
		go func(t *registered) {
			defer t.running.Store(false)
			t.fn(ctx)
		}(t)
	}
}
