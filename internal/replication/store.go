package replication

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// fileName is the single JSON file the Store persists to under StateDir.
const fileName = "replication.json"

// ErrNotFound is returned by Get/Delete when no task with the given id exists.
var ErrNotFound = errors.New("replication task not found")

// Store is a single-file JSON-backed task store. It is safe for concurrent
// use; writes are serialised through tmp+rename for crash safety, matching
// the jobs manager (internal/jobs/manager.go writeRecord).
//
// Single file (not one file per task) because the task count is small and
// the entire set is read at startup to register tasks with the scheduler.
type Store struct {
	path string

	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewStore opens (and creates if absent) a store under stateDir.
// stateDir must already exist; it does not create the parent.
func NewStore(stateDir string) (*Store, error) {
	if stateDir == "" {
		return nil, errors.New("stateDir required")
	}
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	s := &Store{
		path:  filepath.Join(stateDir, fileName),
		tasks: make(map[string]*Task),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", s.path, err)
	}
	var on disk
	if err := json.Unmarshal(data, &on); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	for _, t := range on.Tasks {
		tt := t
		s.tasks[tt.ID] = &tt
	}
	return nil
}

// disk is the on-wire shape of the persisted file. Keeping it as a list (not
// a map) gives stable ordering when humans peek at the JSON.
type disk struct {
	Tasks []Task `json:"tasks"`
}

func (s *Store) saveLocked() error {
	out := disk{Tasks: make([]Task, 0, len(s.tasks))}
	for _, t := range s.tasks {
		out.Tasks = append(out.Tasks, *t)
	}
	sort.Slice(out.Tasks, func(i, j int) bool { return out.Tasks[i].CreatedAt.Before(out.Tasks[j].CreatedAt) })
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// List returns a deep copy of all tasks, ordered by creation time.
func (s *Store) List() []Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Get returns a deep copy of the named task.
func (s *Store) Get(id string) (Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	return *t, nil
}

// Create assigns an ID + timestamps to t and persists it. The provided ID is
// ignored — callers cannot pre-pick IDs. Returns the stored copy.
func (s *Store) Create(t Task) (Task, error) {
	id, err := newID()
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	t.ID = id
	t.CreatedAt = now
	t.UpdatedAt = now
	t.LastRuns = nil

	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[id] = &t
	if err := s.saveLocked(); err != nil {
		delete(s.tasks, id)
		return Task{}, err
	}
	return t, nil
}

// Update applies mutate to the task and persists. Returns ErrNotFound if id
// is unknown. mutate may modify any field except ID/CreatedAt; UpdatedAt is
// refreshed automatically.
func (s *Store) Update(id string, mutate func(*Task)) (Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	prev := *t
	mutate(t)
	t.ID = prev.ID
	t.CreatedAt = prev.CreatedAt
	t.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(); err != nil {
		*t = prev
		return Task{}, err
	}
	return *t, nil
}

// Delete removes a task. Returns ErrNotFound if id is unknown.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return ErrNotFound
	}
	delete(s.tasks, id)
	if err := s.saveLocked(); err != nil {
		s.tasks[id] = t
		return err
	}
	return nil
}

// AppendRun records a run outcome on the task, capped at MaxRunHistory entries.
func (s *Store) AppendRun(id string, rec RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return ErrNotFound
	}
	t.LastRuns = append(t.LastRuns, rec)
	if len(t.LastRuns) > MaxRunHistory {
		t.LastRuns = t.LastRuns[len(t.LastRuns)-MaxRunHistory:]
	}
	return s.saveLocked()
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
