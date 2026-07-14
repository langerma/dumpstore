package logging

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/log/logtest"
	"go.opentelemetry.io/otel/trace"
)

// pipeline builds a slog.Logger that runs through the OTEL log SDK into a
// JournalExporter writing to buf — the production single-producer path.
func pipeline(buf *bytes.Buffer) (*slog.Logger, *sdklog.LoggerProvider) {
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(NewJournalExporter(buf))),
	)
	return slog.New(otelslog.NewHandler("test", otelslog.WithLoggerProvider(provider))), provider
}

var timeRe = regexp.MustCompile(`time=[^ ]+ `)

// TestJournalExporterMatchesTextHandler asserts the exporter output is
// identical to what the pre-OTEL handler produced (a slog.TextHandler line),
// modulo the timestamp, for representative records.
func TestJournalExporterMatchesTextHandler(t *testing.T) {
	cases := []struct {
		name string
		log  func(l *slog.Logger)
	}{
		{"plain", func(l *slog.Logger) { l.Info("request done") }},
		{"attrs", func(l *slog.Logger) {
			l.Info("request", "method", "GET", "path", "/api/pools", "status", 200, "duration_ms", int64(12))
		}},
		{"quoted", func(l *slog.Logger) { l.Warn("odd value", "msg", "has spaces and = signs") }},
		{"error-level", func(l *slog.Logger) { l.Error("boom", "err", "exit status 1") }},
		{"float-bool", func(l *slog.Logger) { l.Info("stats", "ratio", 1.5, "ok", true) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got, want bytes.Buffer
			otelLogger, provider := pipeline(&got)
			tc.log(otelLogger)
			if err := provider.ForceFlush(context.Background()); err != nil {
				t.Fatal(err)
			}
			tc.log(slog.New(slog.NewTextHandler(&want, nil)))

			g := timeRe.ReplaceAllString(got.String(), "")
			w := timeRe.ReplaceAllString(want.String(), "")
			if g != w {
				t.Errorf("output mismatch:\n got: %q\nwant: %q", g, w)
			}
		})
	}
}

// TestJournalExporterPriorityPrefix asserts the syslog <N> prefix appears per
// level when running under the systemd journal.
func TestJournalExporterPriorityPrefix(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "8:12345")
	var buf bytes.Buffer
	logger, provider := pipeline(&buf)

	logger.Debug("d")
	logger.Info("i")
	logger.Warn("w")
	logger.Error("e")
	if err := provider.ForceFlush(context.Background()); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	wantPrefixes := []string{"<7>", "<6>", "<4>", "<3>"}
	if len(lines) != len(wantPrefixes) {
		t.Fatalf("got %d lines, want %d: %q", len(lines), len(wantPrefixes), buf.String())
	}
	for i, p := range wantPrefixes {
		if !strings.HasPrefix(lines[i], p) {
			t.Errorf("line %d: want prefix %s, got %q", i, p, lines[i])
		}
	}
}

// TestJournalExporterTraceCorrelation asserts records carrying span context
// get trace_id/span_id appended to the journald line.
func TestJournalExporterTraceCorrelation(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	spanID, _ := trace.SpanIDFromHex("0123456789abcdef")

	rec := logtest.RecordFactory{
		Body:     otellog.StringValue("hello"),
		Severity: otellog.SeverityInfo,
		TraceID:  traceID,
		SpanID:   spanID,
	}.NewRecord()

	var buf bytes.Buffer
	if err := NewJournalExporter(&buf).Export(context.Background(), []sdklog.Record{rec}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "trace_id=0123456789abcdef0123456789abcdef") ||
		!strings.Contains(out, "span_id=0123456789abcdef") {
		t.Errorf("missing trace correlation: %q", out)
	}
}
