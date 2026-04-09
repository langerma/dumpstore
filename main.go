package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"dumpstore/internal/ansible"
	"dumpstore/internal/api"
	"dumpstore/internal/auth"
	"dumpstore/internal/broker"
	"dumpstore/internal/schema"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	var (
		addr        = flag.String("addr", ":8080", "Listen address (used when --tls is not set)")
		baseDir     = flag.String("dir", "", "Base directory (contains playbooks/ and static/); defaults to executable location")
		debug       = flag.Bool("debug", false, "Enable debug log level")
		showVersion = flag.Bool("version", false, "Print version and exit")
		configPath  = flag.String("config", "/etc/dumpstore/dumpstore.conf", "Config file path")
		setPassword = flag.Bool("set-password", false, "Set admin password and exit")
		tlsFlag     = flag.Bool("tls", false, "Enable HTTPS (requires tls_cert_path and tls_key_path in config)")
		tlsPort     = flag.String("tls-port", "443", "HTTPS listen port")
		httpPort    = flag.String("http-port", "80", "HTTP listen port for redirect to HTTPS (used when --tls is set)")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	if *setPassword {
		if err := auth.SetPassword(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	// When running under systemd with StandardOutput=journal, prepend syslog-style
	// priority prefixes (<N>) so the journal stores the correct PRIORITY field.
	// systemd sets JOURNAL_STREAM when stdout is connected to the journal.
	// Without the prefix, every line lands at PRIORITY=6 (info) regardless of level.
	slog.SetDefault(slog.New(newJournalHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	if *baseDir == "" {
		exe, err := os.Executable()
		if err != nil {
			slog.Error("cannot resolve executable path", "err", err)
			os.Exit(1)
		}
		*baseDir = filepath.Dir(exe)
	}

	if err := checkDeps(*baseDir); err != nil {
		slog.Error("dependency check failed", "err", err)
		os.Exit(1)
	}

	if err := schema.WriteVarsFile(filepath.Join(*baseDir, "playbooks")); err != nil {
		slog.Error("failed to write Ansible vars file", "err", err)
		os.Exit(1)
	}

	cfg, err := auth.LoadConfig(*configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	if cfg.ACMEEnabled {
		if _, err := exec.LookPath("lego"); err != nil {
			slog.Warn("lego not found in PATH — ACME cert issuance/renewal will fail")
		}
	}
	if cfg.PasswordHash == "" {
		slog.Warn("no password configured — binding to loopback only; run with --set-password to configure authentication")
		*addr = "127.0.0.1:8080"
	}
	store := auth.NewSessionStore(cfg.SessionTTL.Duration)
	rl := auth.NewRateLimiter()

	runner := ansible.NewRunner(*baseDir)

	b := broker.New()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	broker.StartPoller(ctx, b)

	apiHandler := api.NewHandler(runner, version, b, cfg, store, *configPath)

	mux := http.NewServeMux()
	auth.RegisterRoutes(mux, cfg, store, rl)
	apiHandler.RegisterRoutes(mux)

	staticDir := filepath.Join(*baseDir, "static")
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	authMW := auth.NewMiddleware(cfg, store)
	handler := requestLogger(authMW.Wrap(mux))

	if *tlsFlag && cfg.TLSCertPath != "" && cfg.TLSKeyPath != "" {
		httpsAddr := ":" + *tlsPort
		srv := &http.Server{Addr: httpsAddr, Handler: handler}

		// HTTP redirect server: plain HTTP → HTTPS.
		redirectAddr := ":" + *httpPort
		redirectSrv := &http.Server{
			Addr: redirectAddr,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := "https://" + r.Host + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			}),
		}

		go func() {
			<-ctx.Done()
			slog.Info("dumpstore shutting down")
			srv.Shutdown(context.Background())        //nolint:errcheck
			redirectSrv.Shutdown(context.Background()) //nolint:errcheck
		}()

		go func() {
			if err := redirectSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("redirect server stopped", "err", err)
			}
		}()

		slog.Info("dumpstore starting (TLS)", "https_addr", httpsAddr, "redirect_addr", redirectAddr, "base", *baseDir)
		if err := srv.ListenAndServeTLS(cfg.TLSCertPath, cfg.TLSKeyPath); err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "err", err)
			os.Exit(1)
		}
	} else {
		srv := &http.Server{Addr: *addr, Handler: handler}

		go func() {
			<-ctx.Done()
			slog.Info("dumpstore shutting down")
			if err := srv.Shutdown(context.Background()); err != nil {
				slog.Error("server shutdown error", "err", err)
			}
		}()

		slog.Info("dumpstore starting", "addr", *addr, "base", *baseDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server stopped", "err", err)
			os.Exit(1)
		}
	}
}

// newReqID returns a random 16-character hex string for request correlation.
func newReqID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// requestLogger wraps a handler and emits one logfmt line per request.
// It generates a unique req_id, stores it in the request context, and includes
// it on the request log line so downstream slog calls can be correlated.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = newReqID()
		}
		r = r.WithContext(api.WithReqID(r.Context(), id))
		w.Header().Set("X-Request-ID", id)

		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		elapsed := time.Since(start)

		level := slog.LevelInfo
		if rw.status >= 500 {
			level = slog.LevelError
		} else if rw.status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "request",
			"req_id", id,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", elapsed.Milliseconds(),
			"remote", r.RemoteAddr,
		)
		api.RecordHTTP(r.Method, r.URL.Path, rw.status, elapsed)
	})
}

// statusRecorder captures the HTTP status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher by forwarding to the underlying ResponseWriter
// if it supports flushing. Required for SSE streaming to work through this middleware.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

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

func newJournalHandler(out io.Writer, opts *slog.HandlerOptions) slog.Handler {
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
	// Inject req_id from context if present (set by requestLogger middleware).
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

func checkDeps(baseDir string) error {
	if _, err := exec.LookPath("ansible-playbook"); err != nil {
		return fmt.Errorf("ansible-playbook not found in PATH: %w", err)
	}
	pbDir := filepath.Join(baseDir, "playbooks")
	if _, err := os.Stat(pbDir); err != nil {
		return fmt.Errorf("playbooks directory not found at %s: %w", pbDir, err)
	}
	staticDir := filepath.Join(baseDir, "static")
	if _, err := os.Stat(staticDir); err != nil {
		return fmt.Errorf("static directory not found at %s: %w", staticDir, err)
	}
	return nil
}
