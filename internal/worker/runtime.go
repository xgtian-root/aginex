// Package worker composes the durable background worker runtime.
package worker

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/xgtian-root/aginex/framework/httpx"
	"github.com/xgtian-root/aginex/framework/jobs"
	postgresjobs "github.com/xgtian-root/aginex/framework/jobs/postgres"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/framework/observability"
	"github.com/xgtian-root/aginex/framework/services"
	"github.com/xgtian-root/aginex/framework/uow"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/platform/auditlog"
	"github.com/xgtian-root/aginex/internal/platform/database"
	"github.com/xgtian-root/aginex/internal/platform/filecleanup"
	"github.com/xgtian-root/aginex/internal/platform/migrate"
	"github.com/xgtian-root/aginex/internal/platform/storage"
	"gorm.io/gorm"
)

const (
	fileCleanupJobType      = filecleanup.JobType
	fileCleanupJobVersionV1 = filecleanup.PayloadVersion1
	fileCleanupJobVersionV2 = filecleanup.PayloadVersion2
)

var ErrJobsDisabled = errors.New("worker requires a durable jobs driver")

// Runtime owns the configured durable job runner.
type Runtime struct {
	runner        *jobs.Runner
	sqlDB         *sql.DB
	cfg           config.Config
	store         storage.Storage
	registry      *module.Registry
	services      services.Runtime
	observability *observability.Recorder
	lifecycle     workerLifecycle
}

// New validates runtime dependencies without modifying database schemas.
func New(ctx context.Context, cfg config.Config, db *gorm.DB) (*Runtime, error) {
	return NewWithModules(ctx, cfg, db)
}

// NewWithModules composes built-in and application-owned durable job handlers.
// It only verifies schema versions; application modules must be migrated by the
// explicit migrate process before the worker starts.
func NewWithModules(
	ctx context.Context,
	cfg config.Config,
	db *gorm.DB,
	applicationModules ...module.Module,
) (*Runtime, error) {
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		return nil, fmt.Errorf("configure observability: %w", err)
	}
	return NewWithModulesAndObservability(
		ctx,
		cfg,
		db,
		recorder,
		applicationModules...,
	)
}

// NewWithModulesAndObservability composes a worker with an explicit
// provider-neutral recorder and no process-global exporter state.
func NewWithModulesAndObservability(
	ctx context.Context,
	cfg config.Config,
	db *gorm.DB,
	recorder *observability.Recorder,
	applicationModules ...module.Module,
) (*Runtime, error) {
	if ctx == nil {
		return nil, errors.New("worker context is required")
	}
	if recorder == nil {
		return nil, errors.New("observability recorder is required")
	}
	cfg = config.WithDefaults(cfg)
	if cfg.Jobs.Driver != "postgres" {
		return nil, fmt.Errorf("%w: configured driver is %q", ErrJobsDisabled, cfg.Jobs.Driver)
	}
	if err := config.Validate(cfg); err != nil {
		return nil, fmt.Errorf("validate worker configuration: %w", err)
	}
	if db == nil {
		return nil, errors.New("worker database is required")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access database connection: %w", err)
	}
	if err := database.InstallObservability(
		db,
		recorder,
		cfg.Database.Driver,
	); err != nil {
		return nil, fmt.Errorf("configure database observability: %w", err)
	}
	if err := migrate.EnsureCurrent(ctx, sqlDB, cfg.Database.Driver); err != nil {
		return nil, fmt.Errorf("check database schema: %w", err)
	}
	if err := postgresjobs.EnsureCurrent(ctx, sqlDB); err != nil {
		return nil, fmt.Errorf("check postgres jobs schema: %w", err)
	}

	objectStore, err := storage.FromConfig(ctx, cfg.Storage, cfg.HTTP.PublicURL)
	if err != nil {
		return nil, fmt.Errorf("configure storage: %w", err)
	}
	objectStore = storage.Observe(
		objectStore,
		cfg.Storage.Driver,
		recorder,
	)
	filesEnabled, err := modulesHaveResource(
		applicationModules,
		"files",
	)
	if err != nil {
		return nil, err
	}
	var cleanupV1, cleanupV2 module.JobHandler
	if filesEnabled {
		cleanup, cleanupErr := filecleanup.New(
			db,
			objectStore,
			filecleanup.Config{
				Provider:      cfg.Storage.Driver,
				SystemActorID: cfg.Jobs.WorkerID,
			},
		)
		if cleanupErr != nil {
			return nil, fmt.Errorf(
				"configure file cleanup handler: %w",
				cleanupErr,
			)
		}
		cleanupV1 = cleanup.Handle
		cleanupV2 = cleanup.HandleV2
	}
	registry, err := composeRegistry(
		cleanupV1,
		cleanupV2,
		applicationModules...,
	)
	if err != nil {
		return nil, err
	}
	if err := registry.EnsureMigrationsCurrent(
		ctx,
		sqlDB,
		module.Dialect(cfg.Database.Driver),
	); err != nil {
		return nil, fmt.Errorf("check worker module migrations: %w", err)
	}

	queue, err := postgresjobs.New(db, postgresjobs.Config{
		LeaseDuration: cfg.Jobs.LeaseDuration,
	})
	if err != nil {
		return nil, fmt.Errorf("configure postgres jobs: %w", err)
	}
	observedQueue, err := jobs.ObserveTransactionalQueue(queue, recorder)
	if err != nil {
		return nil, fmt.Errorf(
			"configure postgres jobs observability: %w",
			err,
		)
	}
	writes, err := uow.New(db, auditlog.Recorder{})
	if err != nil {
		return nil, fmt.Errorf("configure worker unit of work: %w", err)
	}
	runtimeServices, err := services.NewRuntime(
		db,
		writes,
		objectStore,
		observedQueue,
		recorder,
	)
	if err != nil {
		return nil, fmt.Errorf("configure module runtime services: %w", err)
	}
	runner, err := jobs.NewRunner(observedQueue, registry, jobs.RunnerConfig{
		WorkerID:          cfg.Jobs.WorkerID,
		Concurrency:       cfg.Jobs.Concurrency,
		PollInterval:      cfg.Jobs.PollInterval,
		LeaseDuration:     cfg.Jobs.LeaseDuration,
		HeartbeatInterval: cfg.Jobs.LeaseDuration / 3,
		OperationTimeout:  cfg.Jobs.LeaseDuration / 3,
		ShutdownTimeout:   cfg.HTTP.ShutdownGracePeriod,
		Recorder:          recorder,
	})
	if err != nil {
		return nil, fmt.Errorf("configure durable job runner: %w", err)
	}
	return &Runtime{
		runner:        runner,
		sqlDB:         sqlDB,
		cfg:           cfg,
		store:         objectStore,
		registry:      registry,
		services:      runtimeServices,
		observability: recorder,
		lifecycle: workerLifecycle{
			cleanupTimeout: cfg.HTTP.ShutdownGracePeriod,
		},
	}, nil
}

// Ready performs bounded, read-only checks for every dependency required by
// the worker. Deployments may expose this through their preferred process or
// internal HTTP probe without coupling the public API readiness to worker
// availability.
func (runtime *Runtime) Ready(ctx context.Context) error {
	if ctx == nil {
		return errors.New("worker readiness context is required")
	}
	if runtime == nil ||
		runtime.sqlDB == nil ||
		runtime.store == nil ||
		runtime.registry == nil {
		return errors.New("worker runtime is required")
	}
	ctx = runtime.contextWithRuntimeServices(ctx)
	check := func(
		name string,
		timeout time.Duration,
		probe func(context.Context) error,
	) error {
		checkContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		if err := probe(checkContext); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	}
	const timeout = 3 * time.Second
	if err := check(
		"database.ping",
		timeout,
		runtime.sqlDB.PingContext,
	); err != nil {
		return err
	}
	if err := check(
		"database.core-migrations",
		timeout,
		func(ctx context.Context) error {
			return migrate.EnsureCurrent(
				ctx,
				runtime.sqlDB,
				runtime.cfg.Database.Driver,
			)
		},
	); err != nil {
		return err
	}
	if err := check(
		"database.jobs-migrations",
		timeout,
		func(ctx context.Context) error {
			return postgresjobs.EnsureCurrent(ctx, runtime.sqlDB)
		},
	); err != nil {
		return err
	}
	if err := check(
		"database.module-migrations",
		timeout,
		func(ctx context.Context) error {
			return runtime.registry.EnsureMigrationsCurrent(
				ctx,
				runtime.sqlDB,
				module.Dialect(runtime.cfg.Database.Driver),
			)
		},
	); err != nil {
		return err
	}
	checker, ok := runtime.store.(storage.ReadinessChecker)
	if !ok {
		return errors.New(
			"storage: configured provider has no readiness check",
		)
	}
	if err := check(
		"storage",
		timeout,
		checker.CheckReadiness,
	); err != nil {
		return err
	}
	for _, dependency := range runtime.registry.ReadinessChecks() {
		err := check(
			dependency.Name,
			dependency.Timeout,
			dependency.Check,
		)
		if err != nil &&
			dependency.Requirement == module.ReadinessRequired {
			return err
		}
		if err != nil {
			slog.Warn(
				"Optional worker readiness check failed",
				"check", dependency.Name,
				"error", httpx.RedactedValue,
			)
		}
	}
	if err := database.RecordPoolStats(
		runtime.observability,
		runtime.sqlDB,
		runtime.cfg.Database.Driver,
	); err != nil {
		slog.Warn(
			"Database pool metrics unavailable",
			"error", httpx.RedactedValue,
		)
	}
	return nil
}

// Run processes durable jobs until the context is canceled.
func (runtime *Runtime) Run(ctx context.Context) (result error) {
	if runtime == nil || runtime.runner == nil {
		return errors.New("worker runtime is required")
	}
	if err := runtime.Start(ctx); err != nil {
		return err
	}
	defer func() {
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			runtime.cfg.HTTP.ShutdownGracePeriod,
		)
		defer cancel()
		result = errors.Join(
			result,
			runtime.Shutdown(shutdownContext),
		)
	}()
	return runtime.runner.Run(runtime.contextWithRuntimeServices(ctx))
}

func (runtime *Runtime) contextWithRuntimeServices(
	ctx context.Context,
) context.Context {
	if runtime == nil ||
		runtime.services.Database == nil ||
		runtime.services.Storage == nil {
		return ctx
	}
	result, err := services.ContextWithRuntime(ctx, runtime.services)
	if err != nil {
		return ctx
	}
	return result
}

type cleanupModule struct {
	handleV1 module.JobHandler
	handleV2 module.JobHandler
}

func (cleanupModule) Name() string {
	return "file-cleanup"
}

func (item cleanupModule) Register(registry *module.Registry) error {
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    fileCleanupJobType,
		Version: fileCleanupJobVersionV1,
		Handle:  item.handleV1,
	}); err != nil {
		return err
	}
	return registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    fileCleanupJobType,
		Version: fileCleanupJobVersionV2,
		Handle:  item.handleV2,
	})
}

func composeRegistry(
	handleV1 module.JobHandler,
	handleV2 module.JobHandler,
	applicationModules ...module.Module,
) (*module.Registry, error) {
	registry := module.NewRegistry()
	modules := append([]module.Module(nil), applicationModules...)
	if handleV1 != nil || handleV2 != nil {
		if handleV1 == nil || handleV2 == nil {
			return nil, errors.New(
				"compose worker modules: both file cleanup handlers are required",
			)
		}
		modules = append([]module.Module{cleanupModule{
			handleV1: handleV1,
			handleV2: handleV2,
		}}, modules...)
	}
	if err := registry.RegisterModules(modules...); err != nil {
		return nil, fmt.Errorf("compose worker modules: %w", err)
	}
	return registry, nil
}

func modulesHaveResource(
	applicationModules []module.Module,
	resourceName string,
) (bool, error) {
	registry := module.NewRegistry()
	if err := registry.RegisterModules(applicationModules...); err != nil {
		return false, fmt.Errorf(
			"inspect worker application modules: %w",
			err,
		)
	}
	for _, resource := range registry.Resources() {
		if resource.Name == resourceName {
			return true, nil
		}
	}
	return false, nil
}
