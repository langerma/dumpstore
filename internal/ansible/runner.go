package ansible

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TaskResult holds the raw result fields from a single Ansible task (used by RunAndGetStdout).
type TaskResult struct {
	Stdout      string   `json:"stdout"`
	StdoutLines []string `json:"stdout_lines"`
	Stderr      string   `json:"stderr"`
	Msg         string   `json:"msg"`
	Failed      bool     `json:"failed"`
	Changed     bool     `json:"changed"`
	RC          int      `json:"rc"`
}

// TaskStep is a human-readable summary of one task, suitable for the API response.
type TaskStep struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok", "changed", "failed", "skipped"
	Msg    string `json:"msg,omitempty"`
}

// ndjsonLine is the JSON structure emitted by the ndjson callback plugin.
type ndjsonLine struct {
	Task   string `json:"task"`
	Status string `json:"status"`
	Msg    string `json:"msg"`
	Stdout string `json:"stdout"`
}

// PlaybookOutput holds the result of a completed playbook run.
type PlaybookOutput struct {
	steps []TaskStep
}

// Steps returns the ordered list of task steps.
func (o *PlaybookOutput) Steps() []TaskStep {
	return o.steps
}

// Runner executes Ansible playbooks.
type Runner struct {
	PlaybookDir   string
	InventoryPath string
	Timeout       time.Duration // per-playbook timeout; defaults to 5 minutes
	metrics       ansibleMetrics
}

// NewRunner creates a Runner whose paths are relative to baseDir.
// If baseDir is empty it defaults to the directory of the running executable.
func NewRunner(baseDir string) *Runner {
	if baseDir == "" {
		exe, err := os.Executable()
		if err == nil {
			baseDir = filepath.Dir(exe)
		} else {
			baseDir = "."
		}
	}
	return &Runner{
		PlaybookDir:   filepath.Join(baseDir, "playbooks"),
		InventoryPath: filepath.Join(baseDir, "playbooks", "inventory", "localhost"),
		Timeout:       5 * time.Minute,
		metrics:       *newAnsibleMetrics(),
	}
}

// EmitMetrics writes Ansible metrics in Prometheus text format to w.
func (r *Runner) EmitMetrics(w io.Writer) {
	r.metrics.emitTo(w)
}

// Run executes a playbook and returns the parsed output.
func (r *Runner) Run(playbook string, extraVars map[string]string) (*PlaybookOutput, error) {
	return r.runCore(playbook, extraVars, nil)
}

// RunStreaming executes a playbook, calling onStep for each task as it completes.
func (r *Runner) RunStreaming(playbook string, extraVars map[string]string, onStep func(TaskStep)) (*PlaybookOutput, error) {
	return r.runCore(playbook, extraVars, onStep)
}

func (r *Runner) runCore(playbook string, extraVars map[string]string, onStep func(TaskStep)) (*PlaybookOutput, error) {
	playbookPath := filepath.Join(r.PlaybookDir, playbook)
	playbookLabel := strings.TrimSuffix(playbook, ".yml")

	args := []string{"-i", r.InventoryPath, playbookPath}
	if len(extraVars) > 0 {
		ev, err := json.Marshal(extraVars)
		if err != nil {
			return nil, fmt.Errorf("marshal extra-vars: %w", err)
		}
		args = append(args, "--extra-vars", string(ev))
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ansible-playbook", args...)
	cmd.Env = append(os.Environ(),
		"ANSIBLE_STDOUT_CALLBACK=ndjson",
		"ANSIBLE_FORCE_COLOR=false",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	slog.Debug("ansible-playbook starting", "playbook", playbook, "timeout", r.Timeout)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ansible-playbook: %w", err)
	}

	var steps []TaskStep
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var line ndjsonLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			continue // skip non-JSON lines (deprecation warnings etc.)
		}
		step := TaskStep{Name: line.Task, Status: line.Status, Msg: line.Msg}
		steps = append(steps, step)
		if onStep != nil {
			onStep(step)
		}
	}

	runErr := cmd.Wait()
	elapsed := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		slog.Error("ansible-playbook timed out", "playbook", playbook, "timeout", r.Timeout)
		r.metrics.record(playbookLabel, elapsed, true)
		return nil, fmt.Errorf("ansible-playbook %s: timed out after %s", playbook, r.Timeout)
	}

	out := &PlaybookOutput{steps: steps}

	// Scan for task-level failures — authoritative failure signal.
	for _, s := range steps {
		if s.Status == "failed" || s.Status == "unreachable" {
			slog.Error("ansible-playbook task failed", "playbook", playbook, "duration_ms", elapsed.Milliseconds(), "task", s.Name)
			r.metrics.record(playbookLabel, elapsed, true)
			return out, fmt.Errorf("task %q failed: %s", s.Name, s.Msg)
		}
	}

	// Fallback: non-zero exit but no task-level failure detected.
	if runErr != nil {
		slog.Error("ansible-playbook exited non-zero", "playbook", playbook, "duration_ms", elapsed.Milliseconds(), "err", runErr, "stderr", stderr.String())
		r.metrics.record(playbookLabel, elapsed, true)
		return out, fmt.Errorf("ansible-playbook %s: %w\nstderr: %s", playbook, runErr, stderr.String())
	}

	slog.Info("ansible-playbook done", "playbook", playbook, "duration_ms", elapsed.Milliseconds())
	r.metrics.record(playbookLabel, elapsed, false)
	return out, nil
}

// RunAndGetStdout runs a playbook and returns the stdout of the named task.
// It looks up the task by name in the NDJSON step output.
func (r *Runner) RunAndGetStdout(playbook, taskName string, extraVars map[string]string) (string, error) {
	out, err := r.Run(playbook, extraVars)
	if err != nil {
		return "", err
	}
	for _, s := range out.steps {
		if s.Name == taskName {
			return s.Msg, nil
		}
	}
	return "", fmt.Errorf("task %q not found in playbook output", taskName)
}
