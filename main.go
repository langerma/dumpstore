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
	"runtime"
	"syscall"

	"dumpstore/internal/ansible"
	"dumpstore/internal/api"
	"dumpstore/internal/auth"
	"dumpstore/internal/broker"
	"dumpstore/internal/logging"
	"dumpstore/internal/platform"
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
		configPath  = flag.String("config", platform.ConfigDir(runtime.GOOS)+"/dumpstore.conf", "Config file path")
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
	slog.SetDefault(slog.New(logging.NewJournalHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

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
	handler := logging.RequestLogger(authMW.Wrap(mux))

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
