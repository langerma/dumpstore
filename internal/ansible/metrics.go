package ansible

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Exposed on /metrics as ansible_runs_total and ansible_run_duration_seconds.
// Ansible runs typically take 1–10 s; the upper buckets catch slow runs.
var (
	meter          = otel.Meter("dumpstore")
	runsCounter, _ = meter.Int64Counter("ansible_runs",
		metric.WithDescription("Total Ansible playbook runs by playbook and outcome."))
	runDuration, _ = meter.Float64Histogram("ansible_run_duration",
		metric.WithDescription("Ansible playbook run duration histogram."),
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.5, 1, 2.5, 5, 10, 30, 60, 120))
)

// recordRun updates metrics after a completed playbook run.
func recordRun(playbook string, d time.Duration, failed bool) {
	status := "ok"
	if failed {
		status = "failed"
	}
	ctx := context.Background()
	pb := attribute.String("playbook", playbook)
	runsCounter.Add(ctx, 1, metric.WithAttributes(pb, attribute.String("status", status)))
	runDuration.Record(ctx, d.Seconds(), metric.WithAttributes(pb))
}
