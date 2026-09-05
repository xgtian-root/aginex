package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	"github.com/xgtian-root/aginex/backend/framework/httpx"
	"github.com/xgtian-root/aginex/backend/internal/buildinfo"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/platform/database"
	"github.com/xgtian-root/aginex/backend/internal/setup"
)

const apiHostInitializationTimeout = 2 * time.Minute

var errAPIHostProductionDatabaseRequired = errors.New(
	"production API hosting requires PostgreSQL",
)

// RunAPI owns the complete Aginex HTTP process lifecycle for definition.
//
// It loads the durable installation state, serves the one-time browser Setup
// flow when required, initializes a configured application, and starts a
// standard net/http server. Cancel ctx to drain HTTP requests and stop module
// lifecycle hooks under the configured shutdown grace period.
func (definition Definition) RunAPI(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run API context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	configureAPIHostLogger()
	state, err := config.LoadState()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	runtime, err := newAPIHostRuntime(ctx, state, definition)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              state.Config.HTTP.Address,
		Handler:           runtime.Handler(),
		ReadHeaderTimeout: state.Config.HTTP.ReadHeaderTimeout,
		ReadTimeout:       state.Config.HTTP.ReadTimeout,
		WriteTimeout:      state.Config.HTTP.WriteTimeout,
		IdleTimeout:       state.Config.HTTP.IdleTimeout,
		MaxHeaderBytes:    state.Config.HTTP.MaxHeaderBytes,
	}

	serverErrors := make(chan error, 1)
	go func() {
		slog.Info(
			"Aginex API listening",
			"address", state.Config.HTTP.Address,
			"environment", state.Config.Environment,
			"version", buildinfo.Version,
			"commit", buildinfo.Commit,
			"build_date", buildinfo.BuildDate,
			"module_fingerprint", definition.Fingerprint(),
			"mode", state.Status,
		)
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrors <- serveErr
		}
	}()

	var serveErr error
	select {
	case err := <-serverErrors:
		serveErr = fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
	}

	return errors.Join(
		serveErr,
		shutdownAPIHost(
			httpServer,
			runtime,
			state.Config.HTTP.ShutdownGracePeriod,
		),
	)
}

func configureAPIHostLogger() {
	slog.SetDefault(newAPIHostLogger(os.Stdout))
}

func newAPIHostLogger(writer io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(
		writer,
		&slog.HandlerOptions{ReplaceAttr: httpx.RedactAttr},
	))
}

type apiHostRuntime struct {
	handler   http.Handler
	lifecycle apiHostLifecycle
}

func (runtime *apiHostRuntime) Handler() http.Handler {
	return runtime.handler
}

func (runtime *apiHostRuntime) Shutdown(ctx context.Context) error {
	if runtime.lifecycle == nil {
		return nil
	}
	return runtime.lifecycle.Shutdown(ctx)
}

func newAPIHostRuntime(
	ctx context.Context,
	state config.State,
	definition Definition,
) (*apiHostRuntime, error) {
	switch state.Status {
	case config.StatusSetup:
		initializer := apiHostSetupInitializer{
			definition: definition,
			base:       state.Config,
		}
		supervisor, err := setup.New(apiHostSupervisorConfig(
			ctx,
			state.Config,
			setup.ModeSetup,
			nil,
			initializer,
			apiHostInstallationStore{
				path:    state.ConfigFile,
				storage: state.Config.Storage,
			},
		))
		if err != nil {
			return nil, fmt.Errorf("create setup runtime: %w", err)
		}
		return &apiHostRuntime{
			handler:   supervisor.Handler(),
			lifecycle: supervisor,
		}, nil

	case config.StatusConfigured:
		initializationContext, cancel := context.WithTimeout(
			ctx,
			apiHostInitializationTimeout,
		)
		candidate, err := initializeConfiguredAPIHost(
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

		supervisor, err := setup.New(apiHostSupervisorConfig(
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
		return &apiHostRuntime{
			handler: supervisor.Handler(),
			lifecycle: apiHostJoinedLifecycle{
				first:  supervisor,
				second: apiHostLifecycleFunc(candidate.Shutdown),
			},
		}, nil

	default:
		return nil, fmt.Errorf(
			"unsupported configuration state %q",
			state.Status,
		)
	}
}

func apiHostSupervisorConfig(
	ctx context.Context,
	cfg config.Config,
	mode setup.Mode,
	application http.Handler,
	initializer apiHostRuntimeInitializer,
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

type apiHostRuntimeInitializer interface {
	setup.DatabaseTester
	setup.ApplicationInitializer
}

type apiHostInstallationStore struct {
	path    string
	storage config.Storage
}

func (store apiHostInstallationStore) CommitInstallation(
	ctx context.Context,
	installation setup.Installation,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if installation.Version != setup.InstallationVersion ||
		installation.Source != setup.InstallationSourceSetup {
		return errors.New("unsupported setup installation payload")
	}
	configured, err := config.NewManagedInstallationWithStorage(
		config.Database{
			Driver: installation.Database.Driver,
			DSN:    installation.Database.DSN,
		},
		installation.SessionSecret,
		store.storage,
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

type apiHostSetupInitializer struct {
	definition Definition
	base       config.Config
}

var _ apiHostRuntimeInitializer = apiHostSetupInitializer{}

func (initializer apiHostSetupInitializer) TestDatabase(
	ctx context.Context,
	databaseConfig setup.DatabaseConfig,
) error {
	candidate := initializer.base
	candidate.Database = config.Database{
		Driver: databaseConfig.Driver,
		DSN:    databaseConfig.DSN,
	}
	if err := validateAPIHostDatabase(candidate); err != nil {
		return err
	}
	if err := config.Validate(candidate); err != nil {
		return fmt.Errorf("validate database candidate: %w", err)
	}

	db, err := database.OpenContext(ctx, candidate.Database)
	if err != nil {
		return fmt.Errorf("open database candidate: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("access database candidate: %w", err)
	}
	defer sqlDB.Close()
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping database candidate: %w", err)
	}
	return nil
}

func (initializer apiHostSetupInitializer) InitializeApplication(
	ctx context.Context,
	request setup.SetupCompleteRequest,
	reporter setup.ProgressReporter,
) (setup.Candidate, error) {
	candidate := initializer.base
	candidate.Database = config.Database{
		Driver: request.Database.Driver,
		DSN:    request.Database.DSN,
	}
	candidate.Bootstrap = config.Bootstrap{
		AdminEmail:    request.Administrator.Email,
		AdminPassword: request.Administrator.Password,
	}
	metadata, _ := setup.RequestMetadataFromContext(ctx)
	return initializeAPIHostApplication(
		ctx,
		initializer.definition,
		candidate,
		BootstrapOptions{
			Source:                         frameworkaudit.SourceHTTP,
			RequestID:                      metadata.RequestID,
			IPAddress:                      metadata.IPAddress,
			ReplaceAdministratorCredential: true,
		},
		reporter,
	)
}

func initializeConfiguredAPIHost(
	ctx context.Context,
	definition Definition,
	cfg config.Config,
) (setup.Candidate, error) {
	return initializeAPIHostApplication(
		ctx,
		definition,
		cfg,
		BootstrapOptions{Source: frameworkaudit.SourceSystem},
		nil,
	)
}

func initializeAPIHostApplication(
	ctx context.Context,
	definition Definition,
	cfg config.Config,
	bootstrapOptions BootstrapOptions,
	reporter setup.ProgressReporter,
) (setup.Candidate, error) {
	if err := validateAPIHostDatabase(cfg); err != nil {
		return setup.Candidate{}, err
	}
	if err := config.Validate(cfg); err != nil {
		return setup.Candidate{}, fmt.Errorf("validate application configuration: %w", err)
	}
	if reporter != nil {
		reporter.Report(setup.StageValidatingDatabase)
	}

	db, err := database.OpenContext(ctx, cfg.Database)
	if err != nil {
		return setup.Candidate{}, fmt.Errorf("open application database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return setup.Candidate{}, fmt.Errorf("access application database: %w", err)
	}
	closeDatabase := true
	defer func() {
		if closeDatabase {
			_ = sqlDB.Close()
		}
	}()
	if err := sqlDB.PingContext(ctx); err != nil {
		return setup.Candidate{}, fmt.Errorf("ping application database: %w", err)
	}

	if reporter != nil {
		reporter.Report(setup.StageMigrating)
	}
	if err := definition.MigrateUp(ctx, sqlDB, cfg); err != nil {
		return setup.Candidate{}, fmt.Errorf("migrate application database: %w", err)
	}

	if reporter != nil {
		reporter.Report(setup.StageBootstrapping)
	}
	if err := definition.BootstrapWithOptions(
		ctx,
		db,
		cfg,
		bootstrapOptions,
	); err != nil {
		return setup.Candidate{}, fmt.Errorf("bootstrap application access: %w", err)
	}
	var activeAdministrator bool
	if bootstrapOptions.ReplaceAdministratorCredential {
		activeAdministrator, err = definition.HasActiveAdministratorCredentials(
			ctx,
			db,
			cfg.Bootstrap.AdminEmail,
			cfg.Bootstrap.AdminPassword,
		)
	} else {
		activeAdministrator, err = definition.HasActiveAdministrator(ctx, db)
	}
	if err != nil {
		return setup.Candidate{}, err
	}
	if !activeAdministrator {
		return setup.Candidate{}, errors.New("application has no active administrator")
	}
	cfg = withoutAPIHostBootstrapCredentials(cfg)

	if reporter != nil {
		reporter.Report(setup.StageStartingApplication)
	}
	application, err := definition.NewAPIContext(ctx, cfg, db)
	if err != nil {
		return setup.Candidate{}, fmt.Errorf("create application runtime: %w", err)
	}
	if err := application.Start(ctx); err != nil {
		return setup.Candidate{}, fmt.Errorf("start application runtime: %w", err)
	}
	resources := &apiHostApplicationResources{
		application: application,
		database:    sqlDB,
	}
	if err := application.Ready(ctx); err != nil {
		_ = resources.Shutdown(context.WithoutCancel(ctx))
		return setup.Candidate{}, fmt.Errorf("verify application readiness: %w", err)
	}
	closeDatabase = false
	return setup.Candidate{
		Handler:       application.Handler(),
		SessionSecret: cfg.Session.Secret,
		Shutdown:      resources.Shutdown,
	}, nil
}

func validateAPIHostDatabase(cfg config.Config) error {
	if cfg.Environment == "production" && cfg.Database.Driver != "postgres" {
		return errAPIHostProductionDatabaseRequired
	}
	return nil
}

func withoutAPIHostBootstrapCredentials(cfg config.Config) config.Config {
	cfg.Bootstrap = config.Bootstrap{}
	return cfg
}

type apiHostApplicationLifecycle interface {
	Shutdown(context.Context) error
}

type apiHostApplicationResources struct {
	once        sync.Once
	application apiHostApplicationLifecycle
	database    interface{ Close() error }
	err         error
}

func (resources *apiHostApplicationResources) Shutdown(ctx context.Context) error {
	resources.once.Do(func() {
		var applicationErr error
		if resources.application != nil {
			applicationErr = resources.application.Shutdown(ctx)
		}
		var databaseErr error
		if resources.database != nil {
			databaseErr = resources.database.Close()
		}
		resources.err = errors.Join(applicationErr, databaseErr)
	})
	return resources.err
}

type apiHostHTTPRuntime interface {
	Shutdown(context.Context) error
	Close() error
}

type apiHostLifecycle interface {
	Shutdown(context.Context) error
}

func shutdownAPIHost(
	httpServer apiHostHTTPRuntime,
	lifecycle apiHostLifecycle,
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
		wrappedHTTPError = fmt.Errorf("graceful HTTP shutdown: %w", httpErr)
	}
	var wrappedCloseError error
	if closeErr != nil {
		wrappedCloseError = fmt.Errorf("force HTTP close: %w", closeErr)
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

type apiHostLifecycleFunc func(context.Context) error

func (function apiHostLifecycleFunc) Shutdown(ctx context.Context) error {
	return function(ctx)
}

type apiHostJoinedLifecycle struct {
	first  apiHostLifecycle
	second apiHostLifecycle
}

func (lifecycle apiHostJoinedLifecycle) Shutdown(ctx context.Context) error {
	return errors.Join(
		lifecycle.first.Shutdown(ctx),
		lifecycle.second.Shutdown(ctx),
	)
}
