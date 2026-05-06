package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func waitTerminal(t *testing.T, m *Manager, id string) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := m.Get(id)
		if !ok {
			t.Fatalf("job %s vanished", id)
		}
		if j.Status.terminal() && !j.FinishedAt.IsZero() {
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not finish in time", id)
	return Job{}
}

func TestRun_Success(t *testing.T) {
	m := newTestManager(t)
	j, err := m.Run("test", []string{"sh", "-c", "echo hello; echo err >&2; exit 0"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	final := waitTerminal(t, m, j.ID)
	if final.Status != StatusSuccess {
		t.Fatalf("status = %s, want success", final.Status)
	}
	if !strings.Contains(final.Stdout, "hello") {
		t.Errorf("stdout = %q, want to contain hello", final.Stdout)
	}
	if !strings.Contains(final.Stderr, "err") {
		t.Errorf("stderr = %q, want to contain err", final.Stderr)
	}
}

func TestRun_Failure(t *testing.T) {
	m := newTestManager(t)
	j, _ := m.Run("test", []string{"sh", "-c", "exit 7"})
	final := waitTerminal(t, m, j.ID)
	if final.Status != StatusFailed {
		t.Fatalf("status = %s, want failed", final.Status)
	}
	if final.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", final.ExitCode)
	}
}

func TestCancel(t *testing.T) {
	m := newTestManager(t)
	j, _ := m.Run("test", []string{"sh", "-c", "sleep 30"})
	// Give the child a moment to actually start before signalling.
	time.Sleep(50 * time.Millisecond)
	if err := m.Cancel(j.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	final := waitTerminal(t, m, j.ID)
	if final.Status != StatusCancelled {
		t.Fatalf("status = %s, want cancelled", final.Status)
	}
}

func TestPersistence_Reload(t *testing.T) {
	dir := t.TempDir()
	m1, err := NewManager(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := m1.Run("test", []string{"sh", "-c", "exit 0"})
	waitTerminal(t, m1, j.ID)

	// New manager over the same dir loads the record.
	m2, err := NewManager(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m2.Get(j.ID)
	if !ok {
		t.Fatalf("job not reloaded")
	}
	if got.Status != StatusSuccess {
		t.Errorf("reloaded status = %s, want success", got.Status)
	}
}

func TestPersistence_RunningBecomesInterrupted(t *testing.T) {
	dir := t.TempDir()
	jobsDir := filepath.Join(dir, "jobs")
	if err := os.MkdirAll(jobsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	stub := Job{
		ID:        "abc123",
		Type:      "test",
		Args:      []string{"sh", "-c", "true"},
		Status:    StatusRunning,
		StartedAt: time.Now().UTC().Add(-time.Hour),
	}
	data, _ := json.Marshal(stub)
	if err := os.WriteFile(filepath.Join(jobsDir, "abc123.json"), data, 0o640); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.Get("abc123")
	if !ok {
		t.Fatalf("job not loaded")
	}
	if got.Status != StatusInterrupted {
		t.Errorf("status = %s, want interrupted", got.Status)
	}
}

func TestNotifier_FiresOnStartAndFinish(t *testing.T) {
	dir := t.TempDir()
	var events []Status
	notify := func(j Job) { events = append(events, j.Status) }
	m, err := NewManager(dir, notify)
	if err != nil {
		t.Fatal(err)
	}
	j, _ := m.Run("test", []string{"sh", "-c", "exit 0"})
	waitTerminal(t, m, j.ID)
	// at least one running and one terminal event
	if len(events) < 2 {
		t.Fatalf("got %d events, want >= 2: %v", len(events), events)
	}
	if events[0] != StatusRunning {
		t.Errorf("first event = %s, want running", events[0])
	}
	last := events[len(events)-1]
	if last != StatusSuccess {
		t.Errorf("last event = %s, want success", last)
	}
}
