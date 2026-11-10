package main

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"dumpstore/internal/ansible"
	"dumpstore/internal/api"
	"dumpstore/internal/broker"
	"dumpstore/internal/ops"
	otelx "dumpstore/internal/otel"
)

// metricFamily is the compatibility contract of one /metrics family: name,
// type, label keys (sorted, without "le") and histogram bucket bounds
// (including the explicit +Inf bucket).
type metricFamily struct {
	typ     string
	help    string
	labels  []string
	buckets []float64
}

// goldenMetrics is the /metrics exposition as emitted by the hand-written
// Prometheus emitters before #168. Moving onto the OTEL meter must not change
// any of it.
var inf = math.Inf(1)

var goldenMetrics = map[string]metricFamily{
	"process_start_time_seconds":      {typ: "GAUGE", help: "Unix timestamp of the process start time."},
	"go_goroutines":                   {typ: "GAUGE", help: "Number of goroutines that currently exist."},
	"go_memstats_alloc_bytes":         {typ: "GAUGE", help: "Number of bytes allocated and still in use."},
	"go_memstats_alloc_bytes_total":   {typ: "COUNTER", help: "Total number of bytes allocated, even if freed."},
	"go_memstats_sys_bytes":           {typ: "GAUGE", help: "Number of bytes obtained from system."},
	"go_memstats_heap_alloc_bytes":    {typ: "GAUGE", help: "Number of heap bytes allocated and still in use."},
	"go_memstats_heap_sys_bytes":      {typ: "GAUGE", help: "Number of heap bytes obtained from system."},
	"go_memstats_heap_idle_bytes":     {typ: "GAUGE", help: "Number of heap bytes waiting to be used."},
	"go_memstats_heap_inuse_bytes":    {typ: "GAUGE", help: "Number of heap bytes that are in use."},
	"go_memstats_heap_released_bytes": {typ: "GAUGE", help: "Number of heap bytes released to OS."},
	"go_memstats_heap_objects":        {typ: "GAUGE", help: "Number of allocated objects."},
	"go_memstats_mallocs_total":       {typ: "COUNTER", help: "Total number of mallocs."},
	"go_memstats_frees_total":         {typ: "COUNTER", help: "Total number of frees."},
	"go_memstats_next_gc_bytes":       {typ: "GAUGE", help: "Number of heap bytes when next garbage collection will take place."},
	"go_gc_cycles_total":              {typ: "COUNTER", help: "Number of completed GC cycles."},
	"go_gc_pause_ns_total":            {typ: "COUNTER", help: "Cumulative GC stop-the-world pause duration in nanoseconds."},
	"dumpstore_build_info": {typ: "GAUGE", help: "A metric with a constant value of 1 labelled with version info.",
		labels: []string{"goversion", "version"}},
	"http_requests_total": {typ: "COUNTER", help: "Total HTTP requests by method, path, and status code.",
		labels: []string{"method", "path", "status"}},
	"http_request_duration_seconds": {typ: "HISTOGRAM", help: "HTTP request latency histogram.",
		labels:  []string{"method", "path"},
		buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, inf}},
	"ansible_runs_total": {typ: "COUNTER", help: "Total Ansible playbook runs by playbook and outcome.",
		labels: []string{"playbook", "status"}},
	"ansible_run_duration_seconds": {typ: "HISTOGRAM", help: "Ansible playbook run duration histogram.",
		labels:  []string{"playbook"},
		buckets: []float64{0.5, 1, 2.5, 5, 10, 30, 60, 120, inf}},
	"dumpstore_op_runs_total": {typ: "COUNTER", help: "In-process write operations by command and status.",
		labels: []string{"op", "status"}},
	"dumpstore_op_duration_seconds": {typ: "HISTOGRAM", help: "Duration of in-process write operations.",
		labels:  []string{"op"},
		buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 5, 30, 120, inf}},
}

// TestMetricsCompat records one sample through every dumpstore instrument,
// scrapes the OTEL-backed /metrics handler and checks the exposition against
// goldenMetrics. It lives in package main because the global meter binds to
// the first SetMeterProvider in a test binary: this must be the only Init.
func TestMetricsCompat(t *testing.T) {
	for _, v := range []string{"OTEL_SDK_DISABLED", "OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"} {
		t.Setenv(v, "")
	}

	// Stand-in binaries so the real ansible and ops runners execute and
	// record a run without touching the host.
	bin := t.TempDir()
	for _, name := range []string{"ansible-playbook", "zpool"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	p, err := otelx.Init(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background()) //nolint:errcheck

	h := api.NewHandler(ansible.NewRunner(t.TempDir()), "test", broker.New(), nil, nil, nil, nil, nil, "")
	h.SetMetricsHandler(p.MetricsHandler)

	api.RecordHTTP("GET", "/api/datasets/tank/data", 200, 3*time.Millisecond)
	if _, err := ansible.NewRunner(t.TempDir()).Run(context.Background(), "noop.yml", nil); err != nil {
		t.Fatalf("ansible run: %v", err)
	}
	if _, err := ops.NewRunner().Run(context.Background(),
		[]ops.Step{{Name: "status", Argv: []string{"zpool", "status"}}}, nil); err != nil {
		t.Fatalf("ops run: %v", err)
	}

	mux := http.NewServeMux()
	h.RegisterRoutes(api.NewRegistry(mux))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("/metrics: status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type = %q, want Prometheus text 0.0.4", ct)
	}

	body := rec.Body.String()
	if strings.Contains(body, "target_info") || strings.Contains(body, "otel_scope_") {
		t.Errorf("exposition leaks OTEL resource/scope info:\n%s", body)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	fams, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse /metrics: %v\n%s", err, body)
	}

	got := map[string]metricFamily{}
	for name, mf := range fams {
		f := metricFamily{typ: mf.GetType().String(), help: mf.GetHelp()}
		keys := map[string]bool{}
		for _, m := range mf.GetMetric() {
			for _, lp := range m.GetLabel() {
				keys[lp.GetName()] = true
			}
			if hist := m.GetHistogram(); hist != nil && f.buckets == nil {
				for _, b := range hist.GetBucket() {
					f.buckets = append(f.buckets, b.GetUpperBound())
				}
			}
		}
		for k := range keys {
			f.labels = append(f.labels, k)
		}
		slices.Sort(f.labels)
		got[name] = f
	}

	for name, want := range goldenMetrics {
		g, ok := got[name]
		if !ok {
			t.Errorf("missing family %s", name)
			continue
		}
		if g.typ != want.typ || g.help != want.help ||
			!slices.Equal(g.labels, want.labels) || !slices.Equal(g.buckets, want.buckets) {
			t.Errorf("%s:\n got  %+v\n want %+v", name, g, want)
		}
	}
	for name := range got {
		if _, ok := goldenMetrics[name]; !ok {
			t.Errorf("unexpected family %s (OTEL disabled must add nothing)", name)
		}
	}
}
