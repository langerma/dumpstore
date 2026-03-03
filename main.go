package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"dumpstore/internal/ansible"
	"dumpstore/internal/api"
	"dumpstore/internal/broker"
)

// version is overridden at build time via:
//
//	go build -ldflags "-X main.version=v1.2.3"
var version = "dev"

func main() {
	var (
		addr        = flag.String("addr", ":8080", "Listen address")
		baseDir     = flag.String("dir", "", "Base directory (contains playbooks/ and static/); defaults to executable location")
		debug       = flag.Bool("debug", false, "Enable debug log level")
		showVersion = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}

	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

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

	runner := ansible.NewRunner(*baseDir)

	b := broker.New()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	broker.StartPoller(ctx, b)

	apiHandler := api.NewHandler(runner, version, b)

	mux := http.NewServeMux()
	apiHandler.RegisterRoutes(mux)

	staticDir := filepath.Join(*baseDir, "static")
	mux.Handle("/", http.FileServer(http.Dir(staticDir)))

	srv := &http.Server{Addr: *addr, Handler: requestLogger(mux)}

	// Shut down the HTTP server when the signal context is cancelled.
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

// requestLogger wraps a handler and emits one logfmt line per request.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)

		level := slog.LevelInfo
		if rw.status >= 500 {
			level = slog.LevelError
		} else if rw.status >= 400 {
			level = slog.LevelWarn
		}
		slog.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
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
