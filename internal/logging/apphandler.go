package logging

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	otellog "go.opentelemetry.io/otel/log"

	"dumpstore/internal/api"
)

// NewAppHandler returns the process-wide slog handler: an otelslog bridge into
// the given LoggerProvider (whose processors fan out to journald and, when
// configured, OTLP), wrapped with the level gate and the req_id context
// injection that journalHandler used to do.
func NewAppHandler(provider otellog.LoggerProvider, level slog.Leveler) slog.Handler {
	return &appHandler{
		inner: otelslog.NewHandler("dumpstore", otelslog.WithLoggerProvider(provider)),
		level: level,
	}
}

type appHandler struct {
	inner slog.Handler
	level slog.Leveler
}

func (h *appHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

func (h *appHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := api.ReqIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String("req_id", id))
	}
	return h.inner.Handle(ctx, r)
}

func (h *appHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &appHandler{inner: h.inner.WithAttrs(attrs), level: h.level}
}

func (h *appHandler) WithGroup(name string) slog.Handler {
	return &appHandler{inner: h.inner.WithGroup(name), level: h.level}
}
