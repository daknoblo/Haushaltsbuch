// Command haushaltsbuch is the entry point for the Haushaltsbuch application.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	// tzdata is embedded so that TZ works on the distroless base image, which
	// does not ship system time zone data.
	_ "time/tzdata"

	"github.com/daknoblo/Haushaltsbuch/internal/config"
	"github.com/daknoblo/Haushaltsbuch/internal/logbuf"
	"github.com/daknoblo/Haushaltsbuch/internal/server"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/version"
)

func main() {
	var (
		healthcheck bool
		showVersion bool
	)
	flag.BoolVar(&healthcheck, "healthcheck", false, "probe the local /healthz endpoint and exit")
	flag.BoolVar(&showVersion, "version", false, "print version information and exit")
	flag.Parse()

	cfg := config.Load()

	if healthcheck {
		os.Exit(runHealthcheck(cfg.Addr))
	}
	if showVersion {
		fmt.Printf("haushaltsbuch %s (channel=%s commit=%s date=%s)\n",
			version.Version, version.Channel, version.Commit, version.Date)
		return
	}

	logBuf := logbuf.New(500)
	base := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	logger := slog.New(logbuf.NewHandler(base, logBuf))
	slog.SetDefault(logger)

	if err := run(cfg, logger); err != nil {
		logger.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

// run wires up the store and HTTP server and blocks until a shutdown signal is
// received.
func run(cfg config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.EnsureSeed(); err != nil {
		return fmt.Errorf("seed store: %w", err)
	}

	srv := server.New(st, logger)
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", cfg.Addr, "db", cfg.DBPath,
			"version", version.Version, "channel", version.Channel)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// runHealthcheck performs an HTTP GET against the local /healthz endpoint and
// returns a process exit code (0 on success). It is used by the container
// HEALTHCHECK because the distroless image has no shell or curl.
func runHealthcheck(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "", strings.TrimPrefix(addr, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	url := fmt.Sprintf("http://%s/healthz", net.JoinHostPort(host, port))
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url) //nolint:noctx // short-lived local probe
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusOK {
		return 0
	}
	return 1
}
