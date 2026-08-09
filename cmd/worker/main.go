package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/xgtian-root/aginex/framework/httpx"
	"github.com/xgtian-root/aginex/internal/buildinfo"
	"github.com/xgtian-root/aginex/internal/composition"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/platform/database"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: httpx.RedactAttr,
	})))
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	slog.Info("Aginex worker waiting for API initialization")
	cfg, err := waitForAPIInitialization(
		ctx,
		config.LoadState,
		&http.Client{
			Timeout: workerStartupRequestTimeout,
			CheckRedirect: func(
				*http.Request,
				[]*http.Request,
			) error {
				return http.ErrUseLastResponse
			},
		},
		workerStartupPollInterval,
	)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("wait for API initialization: %w", err)
	}
	db, err := database.OpenContext(ctx, cfg.Database)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("access database connection: %w", err)
	}
	defer sqlDB.Close()

	definition := composition.Definition()
	runtime, err := definition.NewWorker(ctx, cfg, db)
	if err != nil {
		return fmt.Errorf("create worker: %w", err)
	}
	if err := runtime.Start(ctx); err != nil {
		return fmt.Errorf("start worker lifecycle: %w", err)
	}
	if err := runtime.Ready(ctx); err != nil {
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			cfg.HTTP.ShutdownGracePeriod,
		)
		defer cancel()
		return errors.Join(
			fmt.Errorf("worker is not ready: %w", err),
			runtime.Shutdown(shutdownContext),
		)
	}

	slog.Info(
		"Aginex worker started",
		"worker_id", cfg.Jobs.WorkerID,
		"concurrency", cfg.Jobs.Concurrency,
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"build_date", buildinfo.BuildDate,
		"module_fingerprint", definition.Fingerprint(),
	)
	if err := runtime.Run(ctx); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}
	slog.Info("Aginex worker stopped", "worker_id", cfg.Jobs.WorkerID)
	return nil
}
