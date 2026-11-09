package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"dumpstore/internal/ansible"
)

type capture struct {
	mu    sync.Mutex
	steps []Step
}

func (c *capture) publish(topic string, v any) {
	if topic != Topic {
		panic("unexpected topic " + topic)
	}
	c.mu.Lock()
	c.steps = append(c.steps, v.(Step))
	c.mu.Unlock()
}

func step(name string) ansible.TaskStep { return ansible.TaskStep{Name: name, Status: "ok"} }

const idA = "op-aaaaaaaa"

func TestValidID(t *testing.T) {
	for id, want := range map[string]bool{
		"abcdefgh":              true,
		"0123-4567-89ab":        true,
		strings.Repeat("a", 64): true,
		strings.Repeat("a", 65): false,
		"short":                 false,
		"has space x":           false,
		"semi;colon1":           false,
		"":                      false,
	} {
		if got := ValidID(id); got != want {
			t.Errorf("ValidID(%q) = %v, want %v", id, got, want)
		}
	}
	if id := NewID(); !ValidID(id) {
		t.Errorf("NewID() = %q is not valid", id)
	}
}

func TestBeginDuplicateAndInvalid(t *testing.T) {
	r := New(nil)
	if err := r.Begin(idA); err != nil {
		t.Fatal(err)
	}
	if err := r.Begin(idA); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second Begin = %v, want ErrDuplicate", err)
	}
	r.Finish(idA, 200, nil)
	if err := r.Begin(idA); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Begin of retained finished op = %v, want ErrDuplicate", err)
	}
	if err := r.Begin("bad"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Begin(bad) = %v, want ErrInvalidID", err)
	}
}

func TestStepSeqAndPublish(t *testing.T) {
	c := &capture{}
	r := New(c.publish)
	_ = r.Begin(idA)
	for i := range 5 {
		r.Step(idA, step(fmt.Sprint("s", i)))
	}
	op, ok := r.Get(idA, 0)
	if !ok || len(op.Steps) != 5 {
		t.Fatalf("Get = %+v, %v", op, ok)
	}
	for i, s := range op.Steps {
		if s.Seq != i+1 || s.Op != idA || s.Name != fmt.Sprint("s", i) {
			t.Errorf("step %d = %+v", i, s)
		}
		if c.steps[i] != s {
			t.Errorf("published %+v, recorded %+v", c.steps[i], s)
		}
	}
	if op.State != StateRunning {
		t.Errorf("state = %s", op.State)
	}

	// Steps outside a known running operation are published untagged.
	r.Step("", step("orphan"))
	if last := c.steps[len(c.steps)-1]; last.Op != "" || last.Seq != 0 {
		t.Errorf("orphan step = %+v", last)
	}
}

func TestGetAfter(t *testing.T) {
	r := New(nil)
	_ = r.Begin(idA)
	for range 4 {
		r.Step(idA, step("x"))
	}
	for after, want := range map[int]int{-3: 4, 0: 4, 1: 3, 3: 1, 4: 0, 99: 0} {
		op, _ := r.Get(idA, after)
		if len(op.Steps) != want {
			t.Errorf("Get(after=%d) = %d steps, want %d", after, len(op.Steps), want)
		}
		if want > 0 && op.Steps[0].Seq != max(after, 0)+1 {
			t.Errorf("Get(after=%d) first seq = %d", after, op.Steps[0].Seq)
		}
	}
	if _, ok := r.Get("op-unknown1", 0); ok {
		t.Error("Get(unknown) ok")
	}
	// The copy must not alias the record.
	op, _ := r.Get(idA, 0)
	op.Steps[0].Name = "mutated"
	again, _ := r.Get(idA, 0)
	if again.Steps[0].Name == "mutated" {
		t.Error("Get returned aliased steps")
	}
}

func TestFinishOnce(t *testing.T) {
	r := New(nil)
	_ = r.Begin(idA)
	r.Step(idA, step("x"))
	r.Finish(idA, 500, []byte(`{"error":"boom"}`))
	r.Finish(idA, 200, []byte(`{"ok":true}`))
	op, _ := r.Get(idA, 0)
	if op.State != StateFailed || op.HTTPStatus != 500 || string(op.Result) != `{"error":"boom"}` || op.Finished == nil {
		t.Fatalf("op = %+v", op)
	}
	// Steps after Finish are not recorded.
	r.Step(idA, step("late"))
	if op, _ := r.Get(idA, 0); len(op.Steps) != 1 {
		t.Errorf("late step recorded: %+v", op.Steps)
	}

	r2 := New(nil)
	_ = r2.Begin(idA)
	r2.Finish(idA, 204, nil)
	if op, _ := r2.Get(idA, 0); op.State != StateSucceeded || op.Result != nil {
		t.Errorf("204 op = %+v", op)
	}
}

func TestResultCap(t *testing.T) {
	r := New(nil)
	_ = r.Begin(idA)
	big := []byte(`"` + strings.Repeat("x", MaxResultBytes) + `"`)
	r.Finish(idA, 200, big)
	op, _ := r.Get(idA, 0)
	if !op.ResultTruncated || op.Result != nil {
		t.Errorf("truncated=%v len(result)=%d", op.ResultTruncated, len(op.Result))
	}
}

func TestRetentionByCount(t *testing.T) {
	r := New(nil)
	running := "op-running"
	_ = r.Begin(running)
	for i := range MaxRetained + 50 {
		id := fmt.Sprintf("op-%08d", i)
		if err := r.Begin(id); err != nil {
			t.Fatal(err)
		}
		r.Finish(id, 200, nil)
	}
	if _, ok := r.Get(running, 0); !ok {
		t.Error("running op evicted")
	}
	if _, ok := r.Get("op-00000000", 0); ok {
		t.Error("oldest finished op not evicted")
	}
	if _, ok := r.Get(fmt.Sprintf("op-%08d", MaxRetained+49), 0); !ok {
		t.Error("newest finished op evicted")
	}
	r.mu.Lock()
	n := len(r.ops)
	r.mu.Unlock()
	if n > MaxRetained+1 {
		t.Errorf("%d records retained, want <= %d", n, MaxRetained+1)
	}
}

func TestRetentionByAge(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	r := New(nil)
	r.now = func() time.Time { return now }
	_ = r.Begin("op-old-done")
	_ = r.Begin("op-old-running")
	r.Finish("op-old-done", 200, nil)
	now = now.Add(RetainFor + time.Minute)
	_ = r.Begin("op-trigger")
	if _, ok := r.Get("op-old-done", 0); ok {
		t.Error("expired op not evicted")
	}
	if _, ok := r.Get("op-old-running", 0); !ok {
		t.Error("running op evicted by age")
	}
}

func TestContext(t *testing.T) {
	if IDFromContext(context.Background()) != "" {
		t.Error("empty ctx has ID")
	}
	if IDFromContext(WithID(context.Background(), idA)) != idA {
		t.Error("WithID round-trip failed")
	}
}

// TestConcurrent exercises Begin/Step/Get/Finish from many goroutines; run
// with -race -count=N. Each operation's recorded and published steps must
// be contiguous and in order.
func TestConcurrent(t *testing.T) {
	c := &capture{}
	r := New(c.publish)
	const ops, steps = 32, 50
	var wg sync.WaitGroup
	for i := range ops {
		wg.Add(2)
		id := fmt.Sprintf("op-conc-%04d", i)
		if err := r.Begin(id); err != nil {
			t.Fatal(err)
		}
		go func() {
			defer wg.Done()
			for range steps {
				r.Step(id, step("x"))
			}
			r.Finish(id, 200, []byte(`{}`))
		}()
		go func() {
			defer wg.Done()
			for range steps {
				r.Get(id, 0)
				_ = r.Begin(NewID())
			}
		}()
	}
	wg.Wait()
	last := map[string]int{}
	for _, s := range c.steps {
		if s.Seq != last[s.Op]+1 {
			t.Fatalf("op %s: seq %d after %d", s.Op, s.Seq, last[s.Op])
		}
		last[s.Op] = s.Seq
	}
	for i := range ops {
		id := fmt.Sprintf("op-conc-%04d", i)
		op, ok := r.Get(id, 0)
		if !ok {
			continue // may be evicted by the concurrent Begin calls
		}
		if len(op.Steps) != steps || op.State != StateSucceeded {
			t.Errorf("%s: %d steps, state %s", id, len(op.Steps), op.State)
		}
	}
}
