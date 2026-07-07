package ops

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// opsDurationBuckets are histogram boundaries for op duration in seconds.
// In-process execs typically finish well under a second; upper buckets
// catch slow zpool operations.
const opsBucketCount = 8

var opsDurationBuckets = [opsBucketCount]float64{0.05, 0.1, 0.25, 0.5, 1, 5, 30, 120}

type opsSample struct {
	count   int64
	sum     float64
	buckets [opsBucketCount]int64 // non-cumulative per-slot counts
}

type opsMetrics struct {
	mu   sync.Mutex
	runs map[string]int64      // "op|status" → count (status: ok/failed)
	lat  map[string]*opsSample // op → duration sample
}

func newOpsMetrics() *opsMetrics {
	return &opsMetrics{
		runs: make(map[string]int64),
		lat:  make(map[string]*opsSample),
	}
}

func (m *opsMetrics) record(op string, d time.Duration, failed bool) {
	status := "ok"
	if failed {
		status = "failed"
	}
	secs := d.Seconds()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.runs[op+"|"+status]++

	s, ok := m.lat[op]
	if !ok {
		s = &opsSample{}
		m.lat[op] = s
	}
	s.count++
	s.sum += secs
	for i, le := range opsDurationBuckets {
		if secs <= le {
			s.buckets[i]++
			break
		}
	}
}

// EmitMetrics writes op metrics in Prometheus text format to w.
func (r *Runner) EmitMetrics(w io.Writer) {
	m := r.metrics
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Fprintln(w, "# HELP dumpstore_op_runs_total In-process write operations by command and status.")
	fmt.Fprintln(w, "# TYPE dumpstore_op_runs_total counter")
	keys := make([]string, 0, len(m.runs))
	for k := range m.runs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		op, status, _ := strings.Cut(k, "|")
		fmt.Fprintf(w, "dumpstore_op_runs_total{op=%q,status=%q} %d\n", op, status, m.runs[k])
	}

	fmt.Fprintln(w, "# HELP dumpstore_op_duration_seconds Duration of in-process write operations.")
	fmt.Fprintln(w, "# TYPE dumpstore_op_duration_seconds histogram")
	ops := make([]string, 0, len(m.lat))
	for k := range m.lat {
		ops = append(ops, k)
	}
	sort.Strings(ops)
	for _, op := range ops {
		s := m.lat[op]
		var cum int64
		for i, le := range opsDurationBuckets {
			cum += s.buckets[i]
			fmt.Fprintf(w, "dumpstore_op_duration_seconds_bucket{op=%q,le=\"%g\"} %d\n", op, le, cum)
		}
		fmt.Fprintf(w, "dumpstore_op_duration_seconds_bucket{op=%q,le=\"+Inf\"} %d\n", op, s.count)
		fmt.Fprintf(w, "dumpstore_op_duration_seconds_sum{op=%q} %g\n", op, s.sum)
		fmt.Fprintf(w, "dumpstore_op_duration_seconds_count{op=%q} %d\n", op, s.count)
	}
}
