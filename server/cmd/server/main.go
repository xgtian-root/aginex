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
	"time"

	"github.com/xgtian-root/aginex/server/framework/httpx"
	"github.com/xgtian-root/aginex/server/internal/buildinfo"
	"github.com/xgtian-root/aginex/server/internal/composition"
	"github.com/xgtian-root/aginex/server/internal/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: httpx.RedactAttr,
	})))
	if err := run(); err != nil {
		slog.Error("API stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	signalContext, stopSignals := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stopSignals()

	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	cfg := state.Config
	definition := composition.Definition()
	runtime, err := newManagedServerRuntime(
		signalContext,
		state,
		definition,
	)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTP.Address,
		Handler:           runtime.Handler(),
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		MaxHeaderBytes:    cfg.HTTP.MaxHeaderBytes,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info(
			"Aginex API listening",
			"address", cfg.HTTP.Address,
			"environment", cfg.Environment,
			"version", buildinfo.Version,
			"commit", buildinfo.Commit,
			"build_date", buildinfo.BuildDate,
			"module_fingerprint", definition.Fingerprint(),
			"mode", state.Status,
		)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	var serveErr error
	select {
	case err := <-serverErrors:
		serveErr = fmt.Errorf("serve HTTP: %w", err)
	case <-signalContext.Done():
	}

	return errors.Join(
		serveErr,
		shutdownRuntime(
			httpServer,
			runtime,
			cfg.HTTP.ShutdownGracePeriod,
		),
	)
}

type httpRuntime interface {
	Shutdown(context.Context) error
	Close() error
}

type lifecycleRuntime interface {
	Shutdown(context.Context) error
}

func shutdownRuntime(
	httpServer httpRuntime,
	lifecycle lifecycleRuntime,
	gracePeriod time.Duration,
) error {
	drainContext, cancelDrain := context.WithTimeout(
		context.Background(),
		gracePeriod,
	)
	httpErr := httpServer.Shutdown(drainContext)
	cancelDrain()

	var closeErr error
	if httpErr != nil {
		closeErr = httpServer.Close()
	}

	lifecycleContext, cancelLifecycle := context.WithTimeout(
		context.Background(),
		gracePeriod,
	)
	lifecycleErr := lifecycle.Shutdown(lifecycleContext)
	cancelLifecycle()

	var wrappedHTTPError error
	if httpErr != nil {
		wrappedHTTPError = fmt.Errorf(
			"graceful HTTP shutdown: %w",
			httpErr,
		)
	}
	var wrappedCloseError error
	if closeErr != nil {
		wrappedCloseError = fmt.Errorf(
			"force HTTP close: %w",
			closeErr,
		)
	}
	var wrappedLifecycleError error
	if lifecycleErr != nil {
		wrappedLifecycleError = fmt.Errorf(
			"stop application lifecycle: %w",
			lifecycleErr,
		)
	}
	return errors.Join(
		wrappedHTTPError,
		wrappedCloseError,
		wrappedLifecycleError,
	)
}
