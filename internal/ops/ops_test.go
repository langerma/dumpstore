package ops

import (
	"strings"
	"testing"

	"dumpstore/internal/ansible"
)

func TestRunSuccess(t *testing.T) {
	r := NewRunner()
	var seen []string
	res, err := r.Run([]Step{
		{Name: "say hi", Argv: []string{"echo", "hi"}},
		{Name: "say bye", Argv: []string{"echo", "bye"}},
	}, func(ts ansible.TaskStep) { seen = append(seen, ts.Name) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	steps := res.Steps()
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].Status != "changed" || steps[0].Msg != "hi" {
		t.Errorf("step 0: got %+v", steps[0])
	}
	if len(seen) != 2 || seen[0] != "say hi" {
		t.Errorf("onStep callbacks: %v", seen)
	}
}

func TestRunStopsOnFailure(t *testing.T) {
	r := NewRunner()
	res, err := r.Run([]Step{
		{Name: "fail", Argv: []string{"false"}},
		{Name: "never runs", Argv: []string{"echo", "unreachable"}},
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	steps := res.Steps()
	if len(steps) != 1 {
		t.Fatalf("expected run to stop after the failed step, got %d steps", len(steps))
	}
	if steps[0].Status != "failed" {
		t.Errorf("step 0 status: got %q, want failed", steps[0].Status)
	}
}

func TestRunContinueOnError(t *testing.T) {
	r := NewRunner()
	res, err := r.Run([]Step{
		{Name: "fail one", Argv: []string{"false"}, ContinueOnError: true},
		{Name: "still runs", Argv: []string{"echo", "ok"}, ContinueOnError: true},
	}, nil)
	if err == nil {
		t.Fatal("expected aggregate error")
	}
	if !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("aggregate error message: %v", err)
	}
	if len(res.Steps()) != 2 {
		t.Fatalf("expected both steps to run, got %d", len(res.Steps()))
	}
	if res.Steps()[1].Status != "changed" {
		t.Errorf("step 1 should have succeeded: %+v", res.Steps()[1])
	}
}

func TestRunMissingBinary(t *testing.T) {
	r := NewRunner()
	res, err := r.Run([]Step{
		{Name: "no such tool", Argv: []string{"definitely-not-a-real-binary-xyz"}},
	}, nil)
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if steps := res.Steps(); len(steps) != 1 || steps[0].Status != "failed" || steps[0].Msg == "" {
		t.Errorf("missing binary step: %+v", res.Steps())
	}
}
