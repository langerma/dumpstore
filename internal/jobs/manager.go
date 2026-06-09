package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

const (
	defaultTailSize   = 64 * 1024 // 64 KiB stdout + stderr each
	defaultRetention  = 50        // completed-job records to keep
	cancelGracePeriod = 10 * time.Second
	// collectGracePeriod bounds how long RunPipeline waits for the output
	// collectors to drain after both children exited, before force-closing
	// the pipe read ends (only relevant when a lingering grandchild keeps a
	// write end open).
	collectGracePeriod = 2 * time.Second
)

// Notifier is invoked when a job's state changes (creation, start, finish).
// Implementations must not block; the broker's PublishNoCache satisfies this.
type Notifier func(Job)

// entry wraps a Job with its mutex and cancel handle. Kept private so the
// public Job type stays a pure serialisable value.
type entry struct {
	mu     sync.Mutex
	job    Job
	cancel func() error
}

func (e *entry) snapshot() Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	j := e.job
	j.Args = append([]string(nil), e.job.Args...)
	return j
}

// Manager owns the set of running and completed jobs. Safe for concurrent use.
type Manager struct {
	stateDir string
	tailSize int
	retain   int
	notify   Notifier

	mu      sync.RWMutex
	entries map[string]*entry
}

// NewManager constructs a manager rooted at stateDir/jobs and reloads any
// existing job records. Jobs whose status was "running" at shutdown are
// rewritten as "interrupted" since the child process died with us.
func NewManager(stateDir string, notify Notifier) (*Manager, error) {
	dir := filepath.Join(stateDir, "jobs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create jobs dir: %w", err)
	}
	m := &Manager{
		stateDir: dir,
		tailSize: defaultTailSize,
		retain:   defaultRetention,
		notify:   notify,
		entries:  make(map[string]*entry),
	}
	if err := m.loadFromDisk(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) loadFromDisk() error {
	dirEntries, err := os.ReadDir(m.stateDir)
	if err != nil {
		return fmt.Errorf("read jobs dir: %w", err)
	}
	for _, de := range dirEntries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		path := filepath.Join(m.stateDir, de.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("jobs: read record failed", "path", path, "err", err)
			continue
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			slog.Warn("jobs: parse record failed", "path", path, "err", err)
			continue
		}
		if j.Status == StatusRunning || j.Status == StatusPending {
			j.Status = StatusInterrupted
			if j.FinishedAt.IsZero() {
				j.FinishedAt = time.Now().UTC()
			}
			if j.Error == "" {
				j.Error = "service restarted while job was running"
			}
			_ = m.writeRecord(j)
		}
		m.entries[j.ID] = &entry{job: j}
	}
	m.pruneLocked()
	return nil
}

// Run spawns argv as a new child process in its own process group and
// returns immediately with the created Job (status=running). The actual
// process is supervised by a background goroutine that updates the job
// record on completion. argv[0] must be an executable.
func (m *Manager) Run(jobType string, argv []string) (Job, error) {
	if len(argv) == 0 {
		return Job{}, errors.New("argv required")
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	e := &entry{
		job: Job{
			ID:        id,
			Type:      jobType,
			Args:      append([]string(nil), argv...),
			Status:    StatusRunning,
			StartedAt: time.Now().UTC(),
		},
	}

	if err := cmd.Start(); err != nil {
		cancel()
		_ = stdoutW.Close()
		_ = stderrW.Close()
		return Job{}, fmt.Errorf("start: %w", err)
	}

	pid := cmd.Process.Pid
	// Cancel sends SIGTERM to the whole process group, then escalates to
	// SIGKILL after a grace window. Killing the group rather than the leader
	// catches `bash -c "zfs send | zfs recv"` whose pipeline children would
	// otherwise survive the wrapper's death.
	e.cancel = func() error {
		pgid, err := syscall.Getpgid(pid)
		if err != nil {
			return err
		}
		if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
			return err
		}
		go func() {
			time.Sleep(cancelGracePeriod)
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			cancel()
		}()
		return nil
	}

	m.mu.Lock()
	m.entries[id] = e
	m.mu.Unlock()
	snap := e.snapshot()
	if err := m.writeRecord(snap); err != nil {
		slog.Warn("jobs: persist failed", "id", id, "err", err)
	}
	m.fire(snap)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); m.collect(e, stdoutR, true) }()
	go func() { defer wg.Done(); m.collect(e, stderrR, false) }()

	go func() {
		err := cmd.Wait()
		_ = stdoutW.Close()
		_ = stderrW.Close()
		wg.Wait()
		cancel()
		m.finish(e, err)
	}()

	return snap, nil
}

// RunPipeline spawns `left | right` as two child processes connected by an
// OS pipe, with no shell involved. Both children are placed in the same
// process group so cancel signals reach both. The job's recorded result
// follows pipefail semantics: a non-zero exit on the left (the producer)
// takes precedence over the right's status, since a failed `zfs send`
// usually causes a derived failure on `zfs recv`.
//
// Args is recorded as left + ["|"] + right purely for display.
func (m *Manager) RunPipeline(jobType string, left, right []string) (Job, error) {
	if len(left) == 0 || len(right) == 0 {
		return Job{}, errors.New("both left and right argv required")
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}

	dataR, dataW, err := os.Pipe()
	if err != nil {
		return Job{}, fmt.Errorf("data pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		dataR.Close()
		dataW.Close()
		return Job{}, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		dataR.Close()
		dataW.Close()
		stdoutR.Close()
		stdoutW.Close()
		return Job{}, fmt.Errorf("stderr pipe: %w", err)
	}

	// Callers build argv from validated input (see validZFSName / validRemoteSpec
	// in internal/api). No shell is involved — argv[0] is a fixed binary name
	// and argv[1:] are passed as discrete arguments, so shell metacharacters
	// cannot reach a parser. CodeQL's taint tracker can't see the validators.
	leftCmd := exec.Command(left[0], left[1:]...) //nolint:gosec // G204: argv pre-validated, no shell
	leftCmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	leftCmd.Stdout = dataW
	leftCmd.Stderr = stderrW

	rightCmd := exec.Command(right[0], right[1:]...) //nolint:gosec // G204: argv pre-validated, no shell
	rightCmd.Stdin = dataR
	rightCmd.Stdout = stdoutW
	rightCmd.Stderr = stderrW

	// closeAll closes every pipe end the parent still holds; used on early-exit.
	closeAll := func() {
		_ = dataR.Close()
		_ = dataW.Close()
		_ = stdoutR.Close()
		_ = stdoutW.Close()
		_ = stderrR.Close()
		_ = stderrW.Close()
	}

	if err := leftCmd.Start(); err != nil {
		closeAll()
		return Job{}, fmt.Errorf("start %s: %w", left[0], err)
	}
	rightCmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
		Pgid:    leftCmd.Process.Pid,
	}
	if err := rightCmd.Start(); err != nil {
		_ = leftCmd.Process.Kill()
		_, _ = leftCmd.Process.Wait()
		closeAll()
		return Job{}, fmt.Errorf("start %s: %w", right[0], err)
	}

	// Parent must close its copies of the children's FDs. The child has its
	// own dup; not closing here means the pipe never sees EOF and `zfs recv`
	// hangs forever waiting for more data after `zfs send` exits.
	_ = dataR.Close()
	_ = dataW.Close()
	_ = stdoutW.Close()
	_ = stderrW.Close()

	leaderPid := leftCmd.Process.Pid
	e := &entry{
		job: Job{
			ID:        id,
			Type:      jobType,
			Args:      append(append([]string{}, left...), append([]string{"|"}, right...)...),
			Status:    StatusRunning,
			StartedAt: time.Now().UTC(),
		},
		cancel: func() error {
			pgid, err := syscall.Getpgid(leaderPid)
			if err != nil {
				return err
			}
			if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
				return err
			}
			go func() {
				time.Sleep(cancelGracePeriod)
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}()
			return nil
		},
	}

	m.mu.Lock()
	m.entries[id] = e
	m.mu.Unlock()
	snap := e.snapshot()
	if err := m.writeRecord(snap); err != nil {
		slog.Warn("jobs: persist failed", "id", id, "err", err)
	}
	m.fire(snap)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); m.collect(e, stdoutR, true) }()
	go func() { defer wg.Done(); m.collect(e, stderrR, false) }()

	go func() {
		leftErr := leftCmd.Wait()
		rightErr := rightCmd.Wait()
		// Both children are dead, so every write end they held is closed and
		// the collect goroutines drain whatever is buffered in the pipes and
		// then see EOF. Closing the read ends before the collectors finish
		// would discard that buffered output (this was a real race: the final
		// stdout tail was sometimes lost on loaded machines). The force-close
		// after a grace window only exists for the pathological case of a
		// lingering grandchild that inherited a write end and keeps the pipe
		// open — without it this goroutine would hang and the job would stay
		// "running" forever.
		drained := make(chan struct{})
		go func() { wg.Wait(); close(drained) }()
		select {
		case <-drained:
		case <-time.After(collectGracePeriod):
		}
		_ = stdoutR.Close()
		_ = stderrR.Close()
		<-drained
		var runErr error
		switch {
		case leftErr != nil:
			runErr = fmt.Errorf("%s: %w", left[0], leftErr)
		case rightErr != nil:
			runErr = fmt.Errorf("%s: %w", right[0], rightErr)
		}
		m.finish(e, runErr)
	}()

	return snap, nil
}

func (m *Manager) collect(e *entry, r io.Reader, stdout bool) {
	buf := make([]byte, 4096)
	var tail []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			tail = append(tail, buf[:n]...)
			if len(tail) > m.tailSize {
				tail = tail[len(tail)-m.tailSize:]
			}
			e.mu.Lock()
			if stdout {
				e.job.Stdout = string(tail)
			} else {
				e.job.Stderr = string(tail)
			}
			e.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (m *Manager) finish(e *entry, runErr error) {
	e.mu.Lock()
	e.job.FinishedAt = time.Now().UTC()
	switch {
	case e.job.Status == StatusCancelled:
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			e.job.ExitCode = exitErr.ExitCode()
		}
	case runErr == nil:
		e.job.Status = StatusSuccess
		e.job.ExitCode = 0
	default:
		e.job.Status = StatusFailed
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			e.job.ExitCode = exitErr.ExitCode()
		} else {
			e.job.ExitCode = -1
		}
		e.job.Error = runErr.Error()
	}
	e.mu.Unlock()

	snap := e.snapshot()
	if err := m.writeRecord(snap); err != nil {
		slog.Warn("jobs: persist on finish failed", "id", snap.ID, "err", err)
	}
	m.fire(snap)

	m.mu.Lock()
	m.pruneLocked()
	m.mu.Unlock()
}

// Cancel signals a running job. Returns an error if the id is unknown or the
// job is already terminal.
func (m *Manager) Cancel(id string) error {
	m.mu.RLock()
	e, ok := m.entries[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("job %q not found", id)
	}
	e.mu.Lock()
	if e.job.Status.terminal() {
		current := e.job.Status
		e.mu.Unlock()
		return fmt.Errorf("job %q already %s", id, current)
	}
	e.job.Status = StatusCancelled
	cancel := e.cancel
	e.mu.Unlock()
	if cancel == nil {
		return errors.New("no cancel handle (job not started)")
	}
	if err := cancel(); err != nil {
		return fmt.Errorf("signal: %w", err)
	}
	return nil
}

// Remove drops a terminal job from the manager and deletes its on-disk
// record. Refuses to remove a job that is still running — cancel it first.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("job %q not found", id)
	}
	e.mu.Lock()
	if !e.job.Status.terminal() {
		current := e.job.Status
		e.mu.Unlock()
		m.mu.Unlock()
		return fmt.Errorf("job %q is %s — cancel it first", id, current)
	}
	e.mu.Unlock()
	delete(m.entries, id)
	m.mu.Unlock()

	path := filepath.Join(m.stateDir, id+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("jobs: remove file failed", "id", id, "err", err)
	}
	return nil
}

// Get returns a snapshot of the named job, or false if unknown.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[id]
	if !ok {
		return Job{}, false
	}
	return e.snapshot(), true
}

// List returns snapshots of all known jobs, newest first by StartedAt.
func (m *Manager) List() []Job {
	m.mu.RLock()
	out := make([]Job, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.snapshot())
	}
	m.mu.RUnlock()
	sort.Slice(out, func(i, k int) bool {
		return out[i].StartedAt.After(out[k].StartedAt)
	})
	return out
}

// pruneLocked drops the oldest terminal jobs above the retention limit.
// Caller must hold m.mu (write).
func (m *Manager) pruneLocked() {
	if len(m.entries) <= m.retain {
		return
	}
	type item struct {
		id string
		t  time.Time
	}
	var done []item
	for id, e := range m.entries {
		e.mu.Lock()
		if e.job.Status.terminal() {
			done = append(done, item{id, e.job.FinishedAt})
		}
		e.mu.Unlock()
	}
	sort.Slice(done, func(i, k int) bool { return done[i].t.Before(done[k].t) })
	excess := len(m.entries) - m.retain
	if excess > len(done) {
		excess = len(done)
	}
	for i := 0; i < excess; i++ {
		id := done[i].id
		delete(m.entries, id)
		path := filepath.Join(m.stateDir, id+".json")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			slog.Warn("jobs: prune remove failed", "id", id, "err", err)
		}
	}
}

func (m *Manager) writeRecord(j Job) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(m.stateDir, j.ID+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *Manager) fire(j Job) {
	if m.notify == nil {
		return
	}
	m.notify(j)
}

func newID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
