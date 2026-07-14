package otel

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	otellog "go.opentelemetry.io/otel/log"
)

func clearOTELEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"OTEL_SDK_DISABLED",
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	} {
		t.Setenv(v, "")
	}
}

// TestDisabledByDefault: without OTLP env vars Init must set up only the
// journald branch and report Enabled()==false.
func TestDisabledByDefault(t *testing.T) {
	clearOTELEnv(t)
	if Enabled() {
		t.Fatal("Enabled() true with clean env")
	}

	var buf bytes.Buffer
	p, err := Init(context.Background(), "test", &buf)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background()) //nolint:errcheck

	if got := len(p.shutdowns); got != 1 {
		t.Errorf("want only the log provider registered, got %d shutdowns", got)
	}
}

// TestSDKDisabledOverride: OTEL_SDK_DISABLED=true wins over a set endpoint.
func TestSDKDisabledOverride(t *testing.T) {
	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	if Enabled() {
		t.Fatal("Enabled() true despite OTEL_SDK_DISABLED")
	}
}

// TestNoLocalLogBranch: a nil logOut (-log-stdout=false) must build a working
// provider with no journald processor, and emitting must not panic.
func TestNoLocalLogBranch(t *testing.T) {
	clearOTELEnv(t)
	p, err := Init(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer p.Shutdown(context.Background()) //nolint:errcheck

	var rec otellog.Record
	rec.SetBody(otellog.StringValue("dropped"))
	rec.SetSeverity(otellog.SeverityInfo)
	p.Logs.Logger("test").Emit(context.Background(), rec) // must not panic
}

// TestOTLPExport drives a span and a log record through Init against an
// httptest OTLP sink and asserts both signals arrive.
func TestOTLPExport(t *testing.T) {
	var mu sync.Mutex
	paths := map[string]int{}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer sink.Close()

	clearOTELEnv(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", sink.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	if !Enabled() {
		t.Fatal("Enabled() false with endpoint set")
	}

	var journal bytes.Buffer
	p, err := Init(context.Background(), "test", &journal)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// One span…
	_, span := Tracer().Start(context.Background(), "test.span")
	span.End()
	// …and one log record through the provider.
	var rec otellog.Record
	rec.SetBody(otellog.StringValue("hello otlp"))
	rec.SetSeverity(otellog.SeverityInfo)
	p.Logs.Logger("test").Emit(context.Background(), rec)

	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if paths["/v1/traces"] == 0 {
		t.Errorf("no trace export seen; got %v", paths)
	}
	if paths["/v1/logs"] == 0 {
		t.Errorf("no log export seen; got %v", paths)
	}
	if !strings.Contains(journal.String(), "hello otlp") {
		t.Errorf("journald branch missed the record: %q", journal.String())
	}
}
