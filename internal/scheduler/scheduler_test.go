package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestTickFiresMatching(t *testing.T) {
	s := New()
	var ran atomic.Int32
	sch, err := Parse("*/5 * * * *")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s.Register("t1", sch, func(ctx context.Context) {
		ran.Add(1)
	})

	// Minute 15 matches */5; minute 16 does not.
	s.tick(context.Background(), time.Date(2026, 5, 24, 10, 15, 0, 0, time.UTC))
	// Wait briefly for goroutine.
	waitFor(t, func() bool { return ran.Load() == 1 })

	s.tick(context.Background(), time.Date(2026, 5, 24, 10, 16, 0, 0, time.UTC))
	time.Sleep(20 * time.Millisecond)
	if got := ran.Load(); got != 1 {
		t.Fatalf("expected 1 run, got %d", got)
	}
}

func TestTickSkipsOverlappingRun(t *testing.T) {
	s := New()
	var ran atomic.Int32
	block := make(chan struct{})
	sch, _ := Parse("* * * * *")
	s.Register("slow", sch, func(ctx context.Context) {
		ran.Add(1)
		<-block
	})

	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)

	// First tick: starts the slow task.
	s.tick(context.Background(), now)
	waitFor(t, func() bool { return ran.Load() == 1 })

	// Second tick while the first is still running: must be skipped.
	s.tick(context.Background(), now.Add(time.Minute))
	time.Sleep(20 * time.Millisecond)
	if got := ran.Load(); got != 1 {
		t.Fatalf("expected overlap to be skipped, got %d runs", got)
	}

	// Let the first finish; a subsequent tick should now fire.
	close(block)
	waitFor(t, func() bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return !s.tasks["slow"].running.Load()
	})
	s.tick(context.Background(), now.Add(2*time.Minute))
	waitFor(t, func() bool { return ran.Load() == 2 })
}

func TestUnregisterStopsFiring(t *testing.T) {
	s := New()
	var ran atomic.Int32
	sch, _ := Parse("* * * * *")
	s.Register("t", sch, func(ctx context.Context) { ran.Add(1) })

	s.tick(context.Background(), time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC))
	waitFor(t, func() bool { return ran.Load() == 1 })

	s.Unregister("t")
	s.tick(context.Background(), time.Date(2026, 5, 24, 10, 1, 0, 0, time.UTC))
	time.Sleep(20 * time.Millisecond)
	if got := ran.Load(); got != 1 {
		t.Fatalf("expected no further runs after Unregister, got %d", got)
	}
}

func TestStartStop(t *testing.T) {
	s := New()
	s.Start(context.Background())
	// Stop should return promptly; we don't wait for any ticks (the first
	// tick is up to ~60s away).
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
