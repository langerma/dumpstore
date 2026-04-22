// Package logging provides the slog handler and HTTP request logging middleware
// used by the dumpstore server.
package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"dumpstore/internal/api"
)

// journalHandler wraps slog.TextHandler and prepends a syslog-style priority
// prefix (<N>) to each log line when running under systemd with
// StandardOutput=journal. systemd parses these prefixes and stores the correct
// PRIORITY field in the journal entry, which Loki/Promtail then uses as the
// log level label. Without the prefix every line lands at PRIORITY=6 (info).
//
// When JOURNAL_STREAM is not set (e.g. terminal), the prefix is omitted so
// output stays human-readable.
type journalHandler struct {
	mu      sync.Mutex
	out     io.Writer
	opts    slog.HandlerOptions
	journal bool // true when stdout is connected to the systemd journal
	pre     []func(slog.Handler) slog.Handler // WithAttrs/WithGroup calls to replay
}

// NewJournalHandler returns a slog.Handler that prepends syslog priority
// prefixes when running under the systemd journal (JOURNAL_STREAM is set).
// Falls back to plain slog.TextHandler otherwise.
func NewJournalHandler(out io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	if os.Getenv("JOURNAL_STREAM") == "" {
		// Not under systemd — plain text output, no prefix noise.
		return slog.NewTextHandler(out, opts)
	}
	return &journalHandler{out: out, opts: *opts, journal: true}
}

func (h *journalHandler) Enabled(_ context.Context, l slog.Level) bool {
	min := slog.LevelInfo
	if h.opts.Level != nil {
		min = h.opts.Level.Level()
	}
	return l >= min
}

func (h *journalHandler) Handle(ctx context.Context, r slog.Record) error {
	// Inject req_id from context if present (set by RequestLogger middleware).
	if id := api.ReqIDFromContext(ctx); id != "" {
		r.AddAttrs(slog.String("req_id", id))
	}
	// Build the formatted line into a buffer using a temporary TextHandler.
	// WithAttrs/WithGroup calls are replayed on each Handle so attrs are included.
	var buf bytes.Buffer
	var sh slog.Handler = slog.NewTextHandler(&buf, &h.opts)
	for _, fn := range h.pre {
		sh = fn(sh)
	}
	if err := sh.Handle(ctx, r); err != nil {
		return err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.journal {
		fmt.Fprintf(h.out, "<%d>", journalPriority(r.Level))
	}
	_, err := h.out.Write(buf.Bytes())
	return err
}

func (h *journalHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := h.clone()
	nh.pre = append(nh.pre, func(sh slog.Handler) slog.Handler { return sh.WithAttrs(attrs) })
	return nh
}

func (h *journalHandler) WithGroup(name string) slog.Handler {
	nh := h.clone()
	nh.pre = append(nh.pre, func(sh slog.Handler) slog.Handler { return sh.WithGroup(name) })
	return nh
}

func (h *journalHandler) clone() *journalHandler {
	pre := make([]func(slog.Handler) slog.Handler, len(h.pre))
	copy(pre, h.pre)
	return &journalHandler{out: h.out, opts: h.opts, journal: h.journal, pre: pre}
}

// journalPriority maps slog levels to syslog priority numbers understood by the
// systemd journal: 3=err, 4=warning, 6=info, 7=debug.
func journalPriority(l slog.Level) int {
	switch {
	case l >= slog.LevelError:
		return 3
	case l >= slog.LevelWarn:
		return 4
	case l >= slog.LevelInfo:
		return 6
	default:
		return 7
	}
}
