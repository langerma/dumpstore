package replication

import (
	"testing"
)

func TestStoreRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("fresh store should be empty, got %d", len(got))
	}

	tk, err := s.Create(Task{
		Name:           "nightly",
		Source:         "tank/data",
		Target:         "backup/data",
		Schedule:       "0 3 * * *",
		RetentionCount: 7,
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if tk.ID == "" || tk.CreatedAt.IsZero() {
		t.Fatalf("Create did not assign id/timestamp: %+v", tk)
	}

	// Reopen and verify persistence.
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore reopen: %v", err)
	}
	got, err := s2.Get(tk.ID)
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if got.Name != "nightly" || got.RetentionCount != 7 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	// Update path.
	if _, err := s2.Update(tk.ID, func(t *Task) { t.RetentionCount = 14 }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ = s2.Get(tk.ID)
	if got.RetentionCount != 14 {
		t.Fatalf("update not persisted: %+v", got)
	}
	if got.CreatedAt != tk.CreatedAt {
		t.Fatalf("Update must not change CreatedAt")
	}
	if !got.UpdatedAt.After(tk.UpdatedAt) {
		t.Fatalf("Update must advance UpdatedAt: %v -> %v", tk.UpdatedAt, got.UpdatedAt)
	}

	// Delete.
	if err := s2.Delete(tk.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s2.Get(tk.ID); err != ErrNotFound {
		t.Fatalf("Delete should remove task: %v", err)
	}
}

func TestAppendRunCap(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	tk, _ := s.Create(Task{Name: "n", Source: "a", Target: "b", Schedule: "* * * * *"})

	for i := 0; i < MaxRunHistory+5; i++ {
		if err := s.AppendRun(tk.ID, RunRecord{JobID: "j", Status: "success"}); err != nil {
			t.Fatalf("AppendRun: %v", err)
		}
	}
	got, _ := s.Get(tk.ID)
	if len(got.LastRuns) != MaxRunHistory {
		t.Fatalf("LastRuns not capped: got %d, want %d", len(got.LastRuns), MaxRunHistory)
	}
}
