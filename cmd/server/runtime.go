package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	frameworkapp "github.com/xgtian-root/aginex/framework/application"
	"github.com/xgtian-root/aginex/framework/httpx"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/setup"
)

const configuredInitializationTimeout = 2 * time.Minute

type managedServerRuntime struct {
	handler   http.Handler
	lifecycle lifecycleRuntime
}

func (runtime *managedServerRuntime) Handler() http.Handler {
	return runtime.handler
}

func (runtime *managedServerRuntime) Shutdown(ctx context.Context) error {
	if runtime.lifecycle == nil {
		return nil
	}
	return runtime.lifecycle.Shutdown(ctx)
}

func newManagedServerRuntime(
	ctx context.Context,
	state config.State,
	definition frameworkapp.Definition,
) (*managedServerRuntime, error) {
	switch state.Status {
	case config.StatusSetup:
		initializer := setupApplicationInitializer{
			definition: definition,
			base:       state.Config,
		}
		supervisor, err := setup.New(setupSupervisorConfig(
			ctx,
			state.Config,
			setup.ModeSetup,
			nil,
			initializer,
			managedInstallationStore{path: state.ConfigFile},
		))
		if err != nil {
			return nil, fmt.Errorf("create setup runtime: %w", err)
		}
		return &managedServerRuntime{
			handler:   supervisor.Handler(),
			lifecycle: supervisor,
		}, nil

	case config.StatusConfigured:
		initializationContext, cancel := context.WithTimeout(
			ctx,
			configuredInitializationTimeout,
		)
		candidate, err := initializeConfiguredApplication(
			initializationContext,
			definition,
			state.Config,
		)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("initialize configured application: %w", err)
		}
		cleanupCandidate := true
		defer func() {
			if cleanupCandidate {
				cleanupContext, cleanupCancel := context.WithTimeout(
					context.Background(),
					state.Config.HTTP.ShutdownGracePeriod,
				)
				_ = candidate.Shutdown(cleanupContext)
				cleanupCancel()
			}
		}()

		if state.NeedsEnvironmentMarker {
			if state.Installation == nil {
				return nil, errors.New("configured environment marker is missing")
			}
			if err := config.CommitInstallation(
				state.ConfigFile,
				*state.Installation,
			); err != nil {
				return nil, fmt.Errorf("seal environment installation: %w", err)
			}
		}

		supervisor, err := setup.New(setupSupervisorConfig(
			ctx,
			state.Config,
			setup.ModeApplication,
			candidate.Handler,
			nil,
			nil,
		))
		if err != nil {
			return nil, fmt.Errorf("create application supervisor: %w", err)
		}
		cleanupCandidate = false
		return &managedServerRuntime{
			handler: supervisor.Handler(),
			lifecycle: joinedLifecycle{
				first:  supervisor,
				second: lifecycleFunc(candidate.Shutdown),
			},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported configuration state %q", state.Status)
	}
}

func setupSupervisorConfig(
	ctx context.Context,
	cfg config.Config,
	mode setup.Mode,
	application http.Handler,
	initializer setupRuntimeInitializer,
	store setup.InstallationStore,
) setup.Config {
	result := setup.Config{
		Mode:           mode,
		Application:    application,
		AllowedOrigins: cfg.AllowedWebOrigins(),
		TrustedProxies: cfg.HTTP.TrustedProxies,
		RequestLimits: httpx.RequestLimits{
			MaxBodyBytes:   cfg.HTTP.MaxBodyBytes,
			MaxHeaderBytes: cfg.HTTP.MaxHeaderBytes,
			MaxHeaderCount: cfg.HTTP.MaxHeaderCount,
		},
		CSRFCookieSecure:   cfg.Session.Secure,
		CSRFCookieSameSite: cfg.Session.SameSite,
		CSRFTokenTTL:       cfg.Session.TTL,
		CleanupTimeout:     cfg.HTTP.ShutdownGracePeriod,
		RateLimit: setup.RateLimitConfig{
			Limit:  cfg.RateLimit.LoginLimit,
			Window: cfg.RateLimit.LoginWindow,
		},
		Context: ctx,
	}
	if mode == setup.ModeSetup {
		result.DatabaseTester = initializer
		result.Initializer = initializer
		result.Store = store
	}
	return result
}

type setupRuntimeInitializer interface {
	setup.DatabaseTester
	setup.ApplicationInitializer
}

type managedInstallationStore struct {
	path string
}

func (store managedInstallationStore) CommitInstallation(
	ctx context.Context,
	installation setup.Installation,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if installation.Version != setup.InstallationVersion ||
		installation.Source != "setup" {
		return errors.New("unsupported setup installation payload")
	}
	configured, err := config.NewManagedInstallation(
		config.Database{
			Driver: installation.Database.Driver,
			DSN:    installation.Database.DSN,
		},
		installation.SessionSecret,
	)
	if err != nil {
		return err
	}
	configured.InstalledAt = installation.InstalledAt
	err = config.CommitInstallation(store.path, configured)
	if err != nil && config.InstallationSealed(err) {
		return errors.Join(setup.ErrInstallationSealed, err)
	}
	return err
}

type lifecycleFunc func(context.Context) error

func (function lifecycleFunc) Shutdown(ctx context.Context) error {
	return function(ctx)
}

type joinedLifecycle struct {
	first  lifecycleRuntime
	second lifecycleRuntime
}

func (lifecycle joinedLifecycle) Shutdown(ctx context.Context) error {
	return errors.Join(
		lifecycle.first.Shutdown(ctx),
		lifecycle.second.Shutdown(ctx),
	)
}
