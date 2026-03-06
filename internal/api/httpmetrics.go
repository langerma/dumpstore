package api

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// httpDurationBuckets are the histogram bucket boundaries in seconds.
const httpBucketCount = 11

var httpDurationBuckets = [httpBucketCount]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type httpSample struct {
	count   int64
	sum     float64
	buckets [httpBucketCount]int64 // non-cumulative per-slot counts
}

type httpMetrics struct {
	mu       sync.Mutex
	requests map[string]int64     // "METHOD|path|status" → count
	latency  map[string]*httpSample // "METHOD|path" → sample
}

func newHTTPMetrics() *httpMetrics {
	return &httpMetrics{
		requests: make(map[string]int64),
		latency:  make(map[string]*httpSample),
	}
}

var globalHTTP = newHTTPMetrics()

// RecordHTTP records a completed HTTP request. Called from the requestLogger middleware in main.go.
// Only API paths (/api/... and /metrics) are recorded; static file requests are ignored.
func RecordHTTP(method, path string, status int, d time.Duration) {
	p := normalizePath(path)
	if p == "" {
		return
	}
	globalHTTP.observe(method, p, status, d)
}

// normalizePath returns the normalised path for metric recording, collapsing
// variable segments to keep cardinality low. Returns "" for non-API paths
// (static files, favicons, images) which should not be recorded.
//
//	/api/datasets/tank/data  → /api/datasets/{name}
//	/api/snapshots/tank@s    → /api/snapshots/{name}
//	/api/pools               → /api/pools  (no variable segment)
//	/metrics                 → /metrics
//	/app.js, /favicon.ico, … → ""  (skipped)
func normalizePath(p string) string {
	if p == "/metrics" {
		return p
	}
	if !strings.HasPrefix(p, "/api/") {
		return ""
	}
	// Strip /api/ and split on first remaining slash.
	resource, sub, hasMore := strings.Cut(strings.TrimPrefix(p, "/api/"), "/")
	if hasMore && sub != "" {
		return "/api/" + resource + "/{name}"
	}
	return p
}

func (m *httpMetrics) observe(method, path string, status int, d time.Duration) {
	rk := method + "|" + path + "|" + strconv.Itoa(status)
	lk := method + "|" + path
	secs := d.Seconds()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests[rk]++

	s, ok := m.latency[lk]
	if !ok {
		s = &httpSample{}
		m.latency[lk] = s
	}
	s.count++
	s.sum += secs
	for i, le := range httpDurationBuckets {
		if secs <= le {
			s.buckets[i]++
			break
		}
	}
	// observations above the highest bucket boundary count only in +Inf (via s.count)
}

func (m *httpMetrics) emitTo(w io.Writer) {
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Fprint(w,
		"# HELP http_requests_total Total HTTP requests by method, path, and status code.\n"+
			"# TYPE http_requests_total counter\n")
	for key, count := range m.requests {
		parts := strings.SplitN(key, "|", 3)
		fmt.Fprintf(w, "http_requests_total{method=%q,path=%q,status=%q} %d\n",
			parts[0], parts[1], parts[2], count)
	}

	fmt.Fprint(w,
		"# HELP http_request_duration_seconds HTTP request latency histogram.\n"+
			"# TYPE http_request_duration_seconds histogram\n")
	for key, s := range m.latency {
		parts := strings.SplitN(key, "|", 2)
		method, path := parts[0], parts[1]
		cumulative := int64(0)
		for i, le := range httpDurationBuckets {
			cumulative += s.buckets[i]
			fmt.Fprintf(w, "http_request_duration_seconds_bucket{method=%q,path=%q,le=%q} %d\n",
				method, path, strconv.FormatFloat(le, 'f', -1, 64), cumulative)
		}
		fmt.Fprintf(w, "http_request_duration_seconds_bucket{method=%q,path=%q,le=\"+Inf\"} %d\n",
			method, path, s.count)
		fmt.Fprintf(w, "http_request_duration_seconds_sum{method=%q,path=%q} %s\n",
			method, path, strconv.FormatFloat(s.sum, 'f', -1, 64))
		fmt.Fprintf(w, "http_request_duration_seconds_count{method=%q,path=%q} %d\n",
			method, path, s.count)
	}
}
