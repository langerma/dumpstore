package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	sdklog "go.opentelemetry.io/otel/sdk/log"

	"dumpstore/internal/api"
)

func appPipeline(buf *bytes.Buffer, level slog.Leveler) *slog.Logger {
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(NewJournalExporter(buf))),
	)
	return slog.New(NewAppHandler(provider, level))
}

// TestAppHandlerLevelGate asserts the --debug level gate still filters records
// (the otelslog bridge itself does not).
func TestAppHandlerLevelGate(t *testing.T) {
	var buf bytes.Buffer
	logger := appPipeline(&buf, slog.LevelInfo)

	logger.Debug("hidden")
	logger.Info("visible")

	out := buf.String()
	if strings.Contains(out, "hidden") {
		t.Errorf("debug record not filtered at info level: %q", out)
	}
	if !strings.Contains(out, "visible") {
		t.Errorf("info record missing: %q", out)
	}

	buf.Reset()
	appPipeline(&buf, slog.LevelDebug).Debug("now visible")
	if !strings.Contains(buf.String(), "now visible") {
		t.Errorf("debug record missing at debug level: %q", buf.String())
	}
}

// TestAppHandlerReqID asserts the req_id context value set by RequestLogger
// still lands on the log line, as the old journalHandler guaranteed.
func TestAppHandlerReqID(t *testing.T) {
	var buf bytes.Buffer
	logger := appPipeline(&buf, slog.LevelInfo)

	ctx := api.WithReqID(context.Background(), "deadbeef01234567")
	logger.InfoContext(ctx, "request")

	if !strings.Contains(buf.String(), "req_id=deadbeef01234567") {
		t.Errorf("req_id missing from log line: %q", buf.String())
	}
}
