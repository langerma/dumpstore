package api

import (
	"context"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// meter resolves through the global provider; instruments created before
// internal/otel.Init installs the SDK are delegated once it does.
var meter = otel.Meter("dumpstore")

// Exposed on /metrics as http_requests_total and http_request_duration_seconds
// (the Prometheus exporter appends the _total and unit suffixes).
var (
	httpRequests, _ = meter.Int64Counter("http_requests",
		metric.WithDescription("Total HTTP requests by method, path, and status code."))
	httpDuration, _ = meter.Float64Histogram("http_request_duration",
		metric.WithDescription("HTTP request latency histogram."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10))
)

// RecordHTTP records a completed HTTP request under its route: the matched
// ServeMux pattern without the method (e.g. /api/v1/datasets/{name}), or a
// fixed label for API requests that never matched a route. Called from the
// RequestLogger middleware; an empty route (static files) is not recorded.
func RecordHTTP(method, route string, status int, d time.Duration) {
	if route == "" {
		return
	}
	ctx := context.Background()
	httpRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", route),
		attribute.String("status", strconv.Itoa(status)),
	))
	httpDuration.Record(ctx, d.Seconds(), metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("path", route),
	))
}
