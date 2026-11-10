package api

import (
	"context"
	"net/http"
	"runtime"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var processStart = time.Now()

var registerRuntimeOnce sync.Once

// registerRuntimeMetrics registers the go_* / process_* gauges and counters
// plus dumpstore_build_info on the shared meter. One callback reads
// runtime.MemStats once per collection. Names are already in their final
// Prometheus form (no unit, counters end in _total), so the exporter leaves
// them unchanged.
func registerRuntimeMetrics(version string) {
	registerRuntimeOnce.Do(func() {
		gauge := func(name, desc string) metric.Int64ObservableGauge {
			g, _ := meter.Int64ObservableGauge(name, metric.WithDescription(desc))
			return g
		}
		counter := func(name, desc string) metric.Int64ObservableCounter {
			c, _ := meter.Int64ObservableCounter(name, metric.WithDescription(desc))
			return c
		}

		// process_start_time_seconds is the standard way to express uptime in Prometheus;
		// alerting rules compute uptime as (time() - process_start_time_seconds).
		startTime := gauge("process_start_time_seconds", "Unix timestamp of the process start time.")
		goroutines := gauge("go_goroutines", "Number of goroutines that currently exist.")
		alloc := gauge("go_memstats_alloc_bytes", "Number of bytes allocated and still in use.")
		allocTotal := counter("go_memstats_alloc_bytes_total", "Total number of bytes allocated, even if freed.")
		sys := gauge("go_memstats_sys_bytes", "Number of bytes obtained from system.")
		heapAlloc := gauge("go_memstats_heap_alloc_bytes", "Number of heap bytes allocated and still in use.")
		heapSys := gauge("go_memstats_heap_sys_bytes", "Number of heap bytes obtained from system.")
		heapIdle := gauge("go_memstats_heap_idle_bytes", "Number of heap bytes waiting to be used.")
		heapInuse := gauge("go_memstats_heap_inuse_bytes", "Number of heap bytes that are in use.")
		heapReleased := gauge("go_memstats_heap_released_bytes", "Number of heap bytes released to OS.")
		heapObjects := gauge("go_memstats_heap_objects", "Number of allocated objects.")
		mallocs := counter("go_memstats_mallocs_total", "Total number of mallocs.")
		frees := counter("go_memstats_frees_total", "Total number of frees.")
		nextGC := gauge("go_memstats_next_gc_bytes", "Number of heap bytes when next garbage collection will take place.")
		gcCycles := counter("go_gc_cycles_total", "Number of completed GC cycles.")
		gcPause := counter("go_gc_pause_ns_total", "Cumulative GC stop-the-world pause duration in nanoseconds.")
		buildInfo := gauge("dumpstore_build_info", "A metric with a constant value of 1 labelled with version info.")
		buildAttrs := metric.WithAttributes(
			attribute.String("version", version),
			attribute.String("goversion", runtime.Version()),
		)

		_, _ = meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)

			o.ObserveInt64(startTime, processStart.Unix())
			o.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
			o.ObserveInt64(alloc, int64(ms.HeapAlloc))
			o.ObserveInt64(allocTotal, int64(ms.TotalAlloc))
			o.ObserveInt64(sys, int64(ms.Sys))
			o.ObserveInt64(heapAlloc, int64(ms.HeapAlloc))
			o.ObserveInt64(heapSys, int64(ms.HeapSys))
			o.ObserveInt64(heapIdle, int64(ms.HeapIdle))
			o.ObserveInt64(heapInuse, int64(ms.HeapInuse))
			o.ObserveInt64(heapReleased, int64(ms.HeapReleased))
			o.ObserveInt64(heapObjects, int64(ms.HeapObjects))
			o.ObserveInt64(mallocs, int64(ms.Mallocs))
			o.ObserveInt64(frees, int64(ms.Frees))
			o.ObserveInt64(nextGC, int64(ms.NextGC))
			o.ObserveInt64(gcCycles, int64(ms.NumGC))
			o.ObserveInt64(gcPause, int64(ms.PauseTotalNs))
			o.ObserveInt64(buildInfo, 1, buildAttrs)
			return nil
		}, startTime, goroutines, alloc, allocTotal, sys, heapAlloc, heapSys, heapIdle,
			heapInuse, heapReleased, heapObjects, mallocs, frees, nextGC, gcCycles, gcPause, buildInfo)
	})
}

// SetMetricsHandler injects the Prometheus exposition handler built by
// internal/otel (which this package must not import — cycle via
// internal/logging).
func (h *Handler) SetMetricsHandler(m http.Handler) { h.metrics = m }

// getMetrics handles GET /metrics — Prometheus exposition of every
// instrument on the shared OTEL meter.
func (h *Handler) getMetrics(w http.ResponseWriter, r *http.Request) {
	if h.metrics == nil {
		http.Error(w, "metrics unavailable: OpenTelemetry initialisation failed (see log)", http.StatusServiceUnavailable)
		return
	}
	h.metrics.ServeHTTP(w, r)
}
