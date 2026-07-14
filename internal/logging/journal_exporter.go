package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// JournalExporter renders OTEL log records to the journald/stdout format that
// journalHandler produced: a slog.TextHandler line, prefixed with a syslog
// priority (<N>) when running under the systemd journal. It is the "local"
// branch of the single-producer logging pipeline; the OTLP branch exports the
// very same records. Records carrying span context additionally get trace_id
// and span_id attributes so journald lines correlate with traces.
type JournalExporter struct {
	mu      sync.Mutex
	out     io.Writer
	journal bool
}

// NewJournalExporter returns an exporter writing to out. The priority prefix
// is emitted only when JOURNAL_STREAM is set (same detection as
// NewJournalHandler).
func NewJournalExporter(out io.Writer) *JournalExporter {
	return &JournalExporter{out: out, journal: os.Getenv("JOURNAL_STREAM") != ""}
}

// Export renders each record through a slog.TextHandler so the output format
// is identical to the pre-OTEL handler by construction.
func (e *JournalExporter) Export(ctx context.Context, records []sdklog.Record) error {
	for i := range records {
		r := &records[i]

		ts := r.Timestamp()
		if ts.IsZero() {
			ts = r.ObservedTimestamp()
		}
		level := severityToLevel(r.Severity())
		rec := slog.NewRecord(ts, level, r.Body().AsString(), 0)
		r.WalkAttributes(func(kv otellog.KeyValue) bool {
			rec.AddAttrs(slog.Attr{Key: kv.Key, Value: logValueToSlog(kv.Value)})
			return true
		})
		if r.TraceID().IsValid() {
			rec.AddAttrs(
				slog.String("trace_id", r.TraceID().String()),
				slog.String("span_id", r.SpanID().String()),
			)
		}

		var buf bytes.Buffer
		if err := slog.NewTextHandler(&buf, nil).Handle(ctx, rec); err != nil {
			return err
		}

		e.mu.Lock()
		if e.journal {
			fmt.Fprintf(e.out, "<%d>", journalPriority(level))
		}
		_, err := e.out.Write(buf.Bytes())
		e.mu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *JournalExporter) Shutdown(context.Context) error   { return nil }
func (e *JournalExporter) ForceFlush(context.Context) error { return nil }

// severityToLevel inverts the otelslog level transform (severity = level + 9),
// so levels round-trip exactly.
func severityToLevel(s otellog.Severity) slog.Level {
	return slog.Level(int(s) - 9)
}

// logValueToSlog converts an OTEL log value back to a slog value for
// TextHandler rendering.
func logValueToSlog(v otellog.Value) slog.Value {
	switch v.Kind() {
	case otellog.KindBool:
		return slog.BoolValue(v.AsBool())
	case otellog.KindFloat64:
		return slog.Float64Value(v.AsFloat64())
	case otellog.KindInt64:
		return slog.Int64Value(v.AsInt64())
	case otellog.KindString:
		return slog.StringValue(v.AsString())
	case otellog.KindBytes:
		return slog.StringValue(string(v.AsBytes()))
	case otellog.KindSlice:
		vals := v.AsSlice()
		out := make([]any, len(vals))
		for i, sv := range vals {
			out[i] = logValueToSlog(sv).Any()
		}
		return slog.AnyValue(out)
	case otellog.KindMap:
		kvs := v.AsMap()
		attrs := make([]slog.Attr, len(kvs))
		for i, kv := range kvs {
			attrs[i] = slog.Attr{Key: kv.Key, Value: logValueToSlog(kv.Value)}
		}
		return slog.GroupValue(attrs...)
	default:
		return slog.AnyValue(nil)
	}
}
