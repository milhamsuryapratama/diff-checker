// Command server hosts the diff-checker web UI.
//
// Upload two documents, watch the pipeline run, read the report. The
// deterministic checks run without any API key; the AI tier is offered only
// when a provider is actually configured, so the app is fully usable with no
// credentials at all.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/httpx"
	"github.com/milhamsuryapratama/diff-checker/internal/jobs"
)

func main() {
	addr := flag.String("addr", envOr("ADDR", ":8080"), "alamat listen HTTP")
	uploadDir := flag.String("uploads", envOr("UPLOAD_DIR", ""), "direktori unggahan (default: sementara)")
	concurrency := flag.Int("concurrency", envInt("WORKER_CONCURRENCY", 2),
		"jumlah perbandingan yang boleh berjalan bersamaan")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	dir, cleanup, err := resolveUploadDir(*uploadDir)
	if err != nil {
		logger.Error("tidak dapat menyiapkan direktori unggahan", "err", err)
		os.Exit(1)
	}
	defer cleanup()

	registry := models.FromEnv()
	pipeline, err := agentic.New(registry)
	if err != nil {
		logger.Error("gagal menyusun pipeline", "err", err)
		os.Exit(1)
	}

	if registry.Configured() {
		logger.Info("lapisan AI aktif")
	} else {
		// Not a warning: running without credentials is a supported mode, and
		// the deterministic engine is the part of the product that must always
		// work.
		logger.Info("lapisan AI tidak aktif — hanya mesin deterministik",
			"tier_tanpa_key", registry.Missing())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := jobs.NewStore()
	worker := jobs.NewWorker(store, pipeline, *concurrency, logger)
	worker.Start(ctx)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           httpx.New(store, worker, registry, dir, logger).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		// No write timeout: SSE connections stay open for the life of a job,
		// and a deadline here would sever the progress stream mid-comparison.
		IdleTimeout: 2 * time.Minute,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", *addr, "uploads", dir, "concurrency", *concurrency)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Error("server failed", "err", err)
		os.Exit(1)
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown failed", "err", err)
		os.Exit(1)
	}
}

// resolveUploadDir returns the directory uploads are written to.
//
// A temporary directory is the default because uploaded legal documents are
// confidential and the job store is in-memory: keeping the two lifetimes
// identical means nothing outlives the process that could not also be served
// by it.
func resolveUploadDir(configured string) (string, func(), error) {
	if configured != "" {
		if err := os.MkdirAll(configured, 0o700); err != nil {
			return "", nil, err
		}
		return configured, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "diff-checker-*")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
