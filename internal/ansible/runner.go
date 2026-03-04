package ansible

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TaskResult holds the result of a single Ansible task on one host.
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

// PlaybookOutput is the top-level JSON structure from ANSIBLE_STDOUT_CALLBACK=json.
type PlaybookOutput struct {
	Plays []struct {
		Tasks []struct {
			Task struct {
				Name string `json:"name"`
			} `json:"task"`
			Hosts map[string]TaskResult `json:"hosts"`
		} `json:"tasks"`
	} `json:"plays"`
	Stats map[string]struct {
		Failures    int `json:"failures"`
		Unreachable int `json:"unreachable"`
	} `json:"stats"`
}

// Steps returns a flat ordered list of task steps from the output.
func (o *PlaybookOutput) Steps() []TaskStep {
	var steps []TaskStep
	for _, play := range o.Plays {
		for _, task := range play.Tasks {
			// Use first host result (we always target localhost)
			for _, result := range task.Hosts {
				status := "ok"
				switch {
				case result.Failed:
					status = "failed"
				case result.Changed:
					status = "changed"
				}
				msg := result.Msg
				if msg == "" && result.Failed {
					msg = strings.TrimSpace(result.Stderr + " " + result.Stdout)
				}
				steps = append(steps, TaskStep{
					Name:   task.Task.Name,
					Status: status,
					Msg:    strings.TrimSpace(msg),
				})
				break
			}
		}
	}
	return steps
}

// Runner executes Ansible playbooks.
type Runner struct {
	PlaybookDir   string
	InventoryPath string
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
	}
}

// Run executes a playbook and returns the parsed output.
//
// extraVars are marshalled to JSON and passed as --extra-vars. The JSON callback
// is always enabled (ANSIBLE_STDOUT_CALLBACK=json) so the output can be parsed
// regardless of the system's ansible.cfg settings.
//
// Failure detection is two-layered:
//  1. Task-level: scan PlaybookOutput for any host result with Failed=true.
//  2. Process-level: non-zero exit code after successful JSON parse (e.g. unreachable host).
//
// Returns an error if any task failed, including a descriptive message from the task.
func (r *Runner) Run(playbook string, extraVars map[string]string) (*PlaybookOutput, error) {
	playbookPath := filepath.Join(r.PlaybookDir, playbook)

	args := []string{"-i", r.InventoryPath, playbookPath}

	if len(extraVars) > 0 {
		ev, err := json.Marshal(extraVars)
		if err != nil {
			return nil, fmt.Errorf("marshal extra-vars: %w", err)
		}
		args = append(args, "--extra-vars", string(ev))
	}

	cmd := exec.Command("ansible-playbook", args...)
	cmd.Env = append(os.Environ(),
		"ANSIBLE_STDOUT_CALLBACK=json",
		"ANSIBLE_LOAD_CALLBACK_PLUGINS=true",
		"ANSIBLE_FORCE_COLOR=false",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("ansible-playbook starting", "playbook", playbook)
	start := time.Now()
	runErr := cmd.Run()
	elapsed := time.Since(start)

	// Always try to parse the JSON output — Ansible writes it even on failure.
	out, parseErr := parseOutput(stdout.Bytes())
	if parseErr != nil {
		if runErr != nil {
			slog.Error("ansible-playbook failed", "playbook", playbook, "duration_ms", elapsed.Milliseconds(), "err", runErr, "stderr", stderr.String())
			return nil, fmt.Errorf("ansible-playbook %s failed: %w\nstderr: %s", playbook, runErr, stderr.String())
		}
		return nil, fmt.Errorf("parse ansible output: %w", parseErr)
	}

	// Scan task results for failures — this is the authoritative failure signal.
	if err := firstFailure(out); err != nil {
		slog.Error("ansible-playbook task failed", "playbook", playbook, "duration_ms", elapsed.Milliseconds(), "err", err)
		return out, err
	}

	// Fallback: non-zero exit but no task-level failure detected.
	if runErr != nil {
		slog.Error("ansible-playbook exited non-zero", "playbook", playbook, "duration_ms", elapsed.Milliseconds(), "err", runErr)
		return out, fmt.Errorf("ansible-playbook %s: %w", playbook, runErr)
	}

	slog.Info("ansible-playbook done", "playbook", playbook, "duration_ms", elapsed.Milliseconds())
	return out, nil
}

// RunAndGetStdout runs a playbook and returns the stdout of the named task.
func (r *Runner) RunAndGetStdout(playbook, taskName string, extraVars map[string]string) (string, error) {
	out, err := r.Run(playbook, extraVars)
	if err != nil {
		return "", err
	}
	result := findTask(out, taskName)
	if result == nil {
		return "", fmt.Errorf("task %q not found in playbook output", taskName)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// firstFailure returns an error describing the first failed task, or nil.
func firstFailure(out *PlaybookOutput) error {
	for _, play := range out.Plays {
		for _, task := range play.Tasks {
			for _, result := range task.Hosts {
				if !result.Failed {
					continue
				}
				msg := strings.TrimSpace(result.Msg)
				if detail := strings.TrimSpace(result.Stderr + "\n" + result.Stdout); detail != "" {
					if msg == "" {
						msg = detail
					} else {
						msg = msg + ": " + detail
					}
				}
				if msg == "" {
					msg = fmt.Sprintf("exit code %d", result.RC)
				}
				return fmt.Errorf("task %q failed: %s", task.Task.Name, msg)
			}
		}
	}
	return nil
}

// parseOutput extracts the JSON object from ansible-playbook stdout.
// Ansible may emit non-JSON lines before the opening brace (e.g. deprecation
// warnings), so we skip to the first '{' before unmarshalling.
func parseOutput(data []byte) (*PlaybookOutput, error) {
	idx := bytes.IndexByte(data, '{')
	if idx < 0 {
		return nil, fmt.Errorf("no JSON in ansible output: %s", string(data))
	}
	var out PlaybookOutput
	if err := json.Unmarshal(data[idx:], &out); err != nil {
		return nil, fmt.Errorf("unmarshal ansible JSON: %w", err)
	}
	return &out, nil
}

func findTask(out *PlaybookOutput, name string) *TaskResult {
	for _, play := range out.Plays {
		for _, task := range play.Tasks {
			if task.Task.Name == name {
				for _, result := range task.Hosts {
					r := result
					return &r
				}
			}
		}
	}
	return nil
}
