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

	"github.com/joho/godotenv"

	"github.com/milhamsuryapratama/diff-checker/internal/agentic"
	"github.com/milhamsuryapratama/diff-checker/internal/agentic/models"
	"github.com/milhamsuryapratama/diff-checker/internal/httpx"
	"github.com/milhamsuryapratama/diff-checker/internal/jobs"
)

func main() {
	// Loaded before any flag default or registry is resolved, since both read
	// from the environment this populates. godotenv.Load never overrides a
	// variable already set in the real environment, so a deployment's actual
	// env (container config, CI secrets) always wins over a checked-in .env.
	// A missing .env is not an error — it is optional; the deterministic
	// engine needs no configuration at all.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("gagal membaca .env", "err", err)
		os.Exit(1)
	}

	addr := flag.String("addr", envOr("ADDR", ":8080"), "alamat listen HTTP")
	uploadDir := flag.String("uploads", envOr("UPLOAD_DIR", "data/uploads"), "direktori unggahan")
	dbPath := flag.String("db", envOr("DB_PATH", "data/diff-checker.db"), "berkas basis data SQLite")
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

	db, err := jobs.Open(*dbPath)
	if err != nil {
		logger.Error("tidak dapat membuka basis data", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	store, err := jobs.NewStore(db, logger)
	if err != nil {
		logger.Error("tidak dapat memuat job tersimpan", "err", err)
		os.Exit(1)
	}
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
		logger.Info("server listening",
			"addr", *addr, "uploads", dir, "db", *dbPath, "concurrency", *concurrency)
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
// Uploads now outlive the process, because job rows in the database reference
// them: wiping them on shutdown would leave every restored job pointing at a
// file that no longer exists. The directory is created 0700 — these are
// confidential legal documents, and the reason they persist is that the
// comparison they belong to does.
func resolveUploadDir(configured string) (string, func(), error) {
	if configured == "" {
		configured = "data/uploads"
	}
	if err := os.MkdirAll(configured, 0o700); err != nil {
		return "", nil, err
	}
	return configured, func() {}, nil
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
