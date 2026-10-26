// Command pakka runs the Pakka diagnostic harness: a server-side Go service
// exposing bounded read-only diagnostic checks over HTTP. See README.md and
// docs/architecture.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"pakka/internal/config"
	"pakka/internal/diagnostics"
	"pakka/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("pakka failed", "err", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg := config.Load()
	runner := diagnostics.NewRunner(cfg)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.New(runner, cfg.MaxRuns, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("pakka listening", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		log.Info("pakka shutting down")
		return srv.Shutdown(shutdown)
	case err := <-errCh:
		return err
	}
}
