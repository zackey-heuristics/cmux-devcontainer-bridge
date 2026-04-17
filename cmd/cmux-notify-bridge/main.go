// Command cmux-notify-bridge runs an HTTP server that forwards Claude Code hook
// notifications from a devcontainer to the host-side cmux CLI.
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/zackey-heuristics/cmux-devcontainer-bridge/internal/notifier"
	"github.com/zackey-heuristics/cmux-devcontainer-bridge/internal/server"
)

func main() {
	var (
		listen       = flag.String("listen", "127.0.0.1:8765", "TCP address to listen on")
		token        = flag.String("token", "", "Bearer token required on POST /notify (empty = no auth)")
		cmuxBin      = flag.String("cmux-bin", "", "Explicit path to the cmux binary")
		defaultTitle = flag.String("default-title", "Claude Code", "Title substituted when the request omits title")
		maxBodyBytes = flag.Int64("max-body-bytes", 16384, "Maximum request body size in bytes")
		dryRun       = flag.Bool("dry-run", false, "Log argv but do not exec cmux")
		verbose      = flag.Bool("verbose", false, "Enable debug-level logging")
	)
	flag.Parse()

	// Set up structured logging.
	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Resolve the cmux binary (skipped in dry-run mode so the bridge can smoke-test without cmux installed).
	var binPath string
	if !*dryRun {
		var err error
		binPath, err = notifier.ResolveCmuxBin(*cmuxBin)
		if err != nil {
			logger.Error("cannot resolve cmux binary", "err", err)
			os.Exit(1)
		}
	} else {
		binPath = "(dry-run)"
	}

	logger.Info("starting cmux-notify-bridge",
		"listen", *listen,
		"token_set", *token != "",
		"dry_run", *dryRun,
		"cmux_bin", binPath,
	)

	n := notifier.NewCmuxNotifier(notifier.CmuxNotifierConfig{
		BinPath: binPath,
		DryRun:  *dryRun,
		Logger:  logger,
	})

	h := server.NewHandler(server.Config{
		Token:        *token,
		MaxBodyBytes: *maxBodyBytes,
		DefaultTitle: *defaultTitle,
		Notifier:     n,
		Logger:       logger,
	})

	srv := &http.Server{
		Addr:              *listen,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case sig := <-quit:
		logger.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		if err != nil {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := srv.Shutdown(shutdownCtx); err != nil {
		cancel()
		logger.Error("shutdown error", "err", err)
		os.Exit(1)
	}
	cancel()
	logger.Info("server stopped")
}
