package api

import (
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

var processStart = time.Now()

// getMetrics handles GET /metrics — Prometheus text exposition (format 0.0.4).
// Exposes standard go_ / process_ metrics plus a dumpstore_build_info gauge.
func (h *Handler) getMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	// process_start_time_seconds is the standard way to express uptime in Prometheus;
	// alerting rules compute uptime as (time() - process_start_time_seconds).
	gauge(w, "process_start_time_seconds",
		"Unix timestamp of the process start time.",
		float64(processStart.Unix()))

	gauge(w, "go_goroutines",
		"Number of goroutines that currently exist.",
		float64(runtime.NumGoroutine()))

	gauge(w, "go_memstats_alloc_bytes",
		"Number of bytes allocated and still in use.",
		float64(ms.HeapAlloc))

	counter(w, "go_memstats_alloc_bytes_total",
		"Total number of bytes allocated, even if freed.",
		float64(ms.TotalAlloc))

	gauge(w, "go_memstats_sys_bytes",
		"Number of bytes obtained from system.",
		float64(ms.Sys))

	gauge(w, "go_memstats_heap_alloc_bytes",
		"Number of heap bytes allocated and still in use.",
		float64(ms.HeapAlloc))

	gauge(w, "go_memstats_heap_sys_bytes",
		"Number of heap bytes obtained from system.",
		float64(ms.HeapSys))

	gauge(w, "go_memstats_heap_idle_bytes",
		"Number of heap bytes waiting to be used.",
		float64(ms.HeapIdle))

	gauge(w, "go_memstats_heap_inuse_bytes",
		"Number of heap bytes that are in use.",
		float64(ms.HeapInuse))

	gauge(w, "go_memstats_heap_released_bytes",
		"Number of heap bytes released to OS.",
		float64(ms.HeapReleased))

	gauge(w, "go_memstats_heap_objects",
		"Number of allocated objects.",
		float64(ms.HeapObjects))

	counter(w, "go_memstats_mallocs_total",
		"Total number of mallocs.",
		float64(ms.Mallocs))

	counter(w, "go_memstats_frees_total",
		"Total number of frees.",
		float64(ms.Frees))

	gauge(w, "go_memstats_next_gc_bytes",
		"Number of heap bytes when next garbage collection will take place.",
		float64(ms.NextGC))

	counter(w, "go_gc_cycles_total",
		"Number of completed GC cycles.",
		float64(ms.NumGC))

	counter(w, "go_gc_pause_ns_total",
		"Cumulative GC stop-the-world pause duration in nanoseconds.",
		float64(ms.PauseTotalNs))

	// dumpstore-specific
	fmt.Fprintf(w,
		"# HELP dumpstore_build_info A metric with a constant value of 1 labelled with version info.\n"+
			"# TYPE dumpstore_build_info gauge\n"+
			"dumpstore_build_info{version=%q,goversion=%q} 1\n",
		h.version, runtime.Version())

	globalHTTP.emitTo(w)
	h.runner.EmitMetrics(w)
}

func gauge(w http.ResponseWriter, name, help string, val float64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n",
		name, help, name, name, pf(val))
}

func counter(w http.ResponseWriter, name, help string, val float64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %s\n",
		name, help, name, name, pf(val))
}

func pf(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
