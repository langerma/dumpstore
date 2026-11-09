// Package operations tracks write operations (one HTTP write request each)
// so their progress steps can be attributed and their final result fetched
// after a dropped connection (#184).
//
// Every step published on the ansible.progress SSE topic carries the
// operation ID and a per-operation sequence number. Clients filter the
// shared stream by ID and close gaps via Get(id, after). Records live in
// memory only: finished operations are retained up to MaxRetained entries
// and RetainFor age; running operations are never evicted.
package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sync"
	"time"

	"dumpstore/internal/ansible"
)

const (
	// Topic is the SSE topic steps are published on.
	Topic = "ansible.progress"
	// MaxRetained bounds the number of finished operations kept.
	MaxRetained = 200
	// RetainFor bounds how long a finished operation is kept.
	RetainFor = time.Hour
	// MaxResultBytes caps the stored final response body.
	MaxResultBytes = 256 << 10
)

// State values for Op.State.
const (
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
)

var (
	// ErrDuplicate is returned by Begin when the ID is already known.
	ErrDuplicate = errors.New("operation ID already in use")
	// ErrInvalidID is returned by Begin when the ID has the wrong format.
	ErrInvalidID = errors.New("invalid operation ID (want 8-64 chars of [A-Za-z0-9-])")

	reID = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)
)

// ValidID reports whether id is an acceptable client-chosen operation ID.
func ValidID(id string) bool { return reID.MatchString(id) }

// NewID returns a fresh random operation ID.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Step is one progress step of an operation. It is also the payload
// published on the ansible.progress topic.
type Step struct {
	Op     string `json:"op"`
	Seq    int    `json:"seq"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Msg    string `json:"msg,omitempty"`
}

// Op is a snapshot of an operation's record.
type Op struct {
	ID              string          `json:"id"`
	State           string          `json:"state"`
	Started         time.Time       `json:"started"`
	Finished        *time.Time      `json:"finished,omitempty"`
	HTTPStatus      int             `json:"http_status,omitempty"`
	Steps           []Step          `json:"steps"`
	Result          json.RawMessage `json:"result,omitempty"`
	ResultTruncated bool            `json:"result_truncated,omitempty"`
}

// Publisher delivers a payload to SSE subscribers of topic. The broker's
// PublishNoCache satisfies it.
type Publisher func(topic string, v any)

// Registry holds operation records. The zero value is not usable; use New.
type Registry struct {
	mu      sync.Mutex
	ops     map[string]*Op
	order   []string // insertion order, oldest first
	publish Publisher
	now     func() time.Time
}

// New returns a Registry that publishes steps via publish (may be nil).
func New(publish Publisher) *Registry {
	if publish == nil {
		publish = func(string, any) {}
	}
	return &Registry{ops: make(map[string]*Op), publish: publish, now: time.Now}
}

// Begin registers a running operation. It fails with ErrInvalidID or
// ErrDuplicate; on success it also evicts expired finished records.
func (r *Registry) Begin(id string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.ops[id]; ok {
		return ErrDuplicate
	}
	r.evictLocked()
	r.ops[id] = &Op{ID: id, State: StateRunning, Started: r.now(), Steps: []Step{}}
	r.order = append(r.order, id)
	return nil
}

// evictLocked drops finished records older than RetainFor, then the oldest
// finished records beyond MaxRetained. Running records are always kept.
func (r *Registry) evictLocked() {
	cutoff := r.now().Add(-RetainFor)
	finished := 0
	for _, id := range r.order {
		if r.ops[id].State != StateRunning {
			finished++
		}
	}
	kept := r.order[:0]
	for _, id := range r.order {
		op := r.ops[id]
		if op.State != StateRunning && (op.Finished.Before(cutoff) || finished >= MaxRetained) {
			delete(r.ops, id)
			finished--
			continue
		}
		kept = append(kept, id)
	}
	for i := len(kept); i < len(r.order); i++ {
		r.order[i] = ""
	}
	r.order = kept
}

// Step records step under operation id with the next sequence number and
// publishes it. Steps for an unknown or finished operation are published
// untagged (seq 0) so live progress is never lost, but not recorded.
func (r *Registry) Step(id string, step ansible.TaskStep) {
	s := Step{Name: step.Name, Status: step.Status, Msg: step.Msg}
	r.mu.Lock()
	if op, ok := r.ops[id]; ok && op.State == StateRunning {
		s.Op = id
		s.Seq = len(op.Steps) + 1
		op.Steps = append(op.Steps, s)
	}
	// Publish under the lock so subscribers see steps of one operation in
	// seq order; the broker's publish is non-blocking.
	r.publish(Topic, s)
	r.mu.Unlock()
}

// Finish marks operation id done with the final HTTP status and response
// body. Status < 400 means succeeded. Only the first call has an effect.
func (r *Registry) Finish(id string, status int, body []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok || op.State != StateRunning {
		return
	}
	now := r.now()
	op.Finished = &now
	op.HTTPStatus = status
	op.State = StateSucceeded
	if status >= 400 {
		op.State = StateFailed
	}
	switch {
	case len(body) > MaxResultBytes:
		op.ResultTruncated = true
	case len(body) > 0 && json.Valid(body):
		op.Result = append(json.RawMessage(nil), body...)
	}
}

// Get returns a copy of operation id with only the steps whose seq > after.
func (r *Registry) Get(id string, after int) (Op, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return Op{}, false
	}
	c := *op
	if after < 0 {
		after = 0
	}
	if after > len(op.Steps) {
		after = len(op.Steps)
	}
	c.Steps = append([]Step{}, op.Steps[after:]...)
	if op.Finished != nil {
		f := *op.Finished
		c.Finished = &f
	}
	return c, true
}

type ctxKey struct{}

// WithID returns a context carrying operation ID id.
func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// IDFromContext returns the operation ID stored by WithID, or "".
func IDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
