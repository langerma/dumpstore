package ansible

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ansibleDurationBuckets are histogram bucket boundaries for playbook run duration in seconds.
// Ansible runs typically take 1–10 s; the upper buckets catch slow runs.
const ansibleBucketCount = 8

var ansibleDurationBuckets = [ansibleBucketCount]float64{0.5, 1, 2.5, 5, 10, 30, 60, 120}

type ansibleSample struct {
	count   int64
	sum     float64
	buckets [ansibleBucketCount]int64 // non-cumulative per-slot counts
}

type ansibleMetrics struct {
	mu   sync.Mutex
	runs map[string]int64          // "playbook|status" → count  (status: ok/failed)
	lat  map[string]*ansibleSample // playbook → duration sample
}

func newAnsibleMetrics() *ansibleMetrics {
	return &ansibleMetrics{
		runs: make(map[string]int64),
		lat:  make(map[string]*ansibleSample),
	}
}

// record updates metrics after a completed playbook run.
func (m *ansibleMetrics) record(playbook string, d time.Duration, failed bool) {
	status := "ok"
	if failed {
		status = "failed"
	}
	secs := d.Seconds()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.runs[playbook+"|"+status]++

	s, ok := m.lat[playbook]
	if !ok {
		s = &ansibleSample{}
		m.lat[playbook] = s
	}
	s.count++
	s.sum += secs
	for i, le := range ansibleDurationBuckets {
		if secs <= le {
			s.buckets[i]++
			break
		}
	}
	// observations above the highest bucket count only in +Inf (via s.count)
}

func (m *ansibleMetrics) emitTo(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Fprint(w,
		"# HELP ansible_runs_total Total Ansible playbook runs by playbook and outcome.\n"+
			"# TYPE ansible_runs_total counter\n")
	for key, count := range m.runs {
		parts := strings.SplitN(key, "|", 2)
		fmt.Fprintf(w, "ansible_runs_total{playbook=%q,status=%q} %d\n",
			parts[0], parts[1], count)
	}

	fmt.Fprint(w,
		"# HELP ansible_run_duration_seconds Ansible playbook run duration histogram.\n"+
			"# TYPE ansible_run_duration_seconds histogram\n")
	for playbook, s := range m.lat {
		cumulative := int64(0)
		for i, le := range ansibleDurationBuckets {
			cumulative += s.buckets[i]
			fmt.Fprintf(w, "ansible_run_duration_seconds_bucket{playbook=%q,le=%q} %d\n",
				playbook, strconv.FormatFloat(le, 'f', -1, 64), cumulative)
		}
		fmt.Fprintf(w, "ansible_run_duration_seconds_bucket{playbook=%q,le=\"+Inf\"} %d\n",
			playbook, s.count)
		fmt.Fprintf(w, "ansible_run_duration_seconds_sum{playbook=%q} %s\n",
			playbook, strconv.FormatFloat(s.sum, 'f', -1, 64))
		fmt.Fprintf(w, "ansible_run_duration_seconds_count{playbook=%q} %d\n",
			playbook, s.count)
	}
}
