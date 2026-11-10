package ops

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Exposed on /metrics as dumpstore_op_runs_total and
// dumpstore_op_duration_seconds. In-process execs typically finish well under
// a second; upper buckets catch slow zpool operations.
var (
	meter          = otel.Meter("dumpstore")
	runsCounter, _ = meter.Int64Counter("dumpstore_op_runs",
		metric.WithDescription("In-process write operations by command and status."))
	runDuration, _ = meter.Float64Histogram("dumpstore_op_duration",
		metric.WithDescription("Duration of in-process write operations."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.05, 0.1, 0.25, 0.5, 1, 5, 30, 120))
)

func recordRun(op string, d time.Duration, failed bool) {
	status := "ok"
	if failed {
		status = "failed"
	}
	ctx := context.Background()
	o := attribute.String("op", op)
	runsCounter.Add(ctx, 1, metric.WithAttributes(o, attribute.String("status", status)))
	runDuration.Record(ctx, d.Seconds(), metric.WithAttributes(o))
}
