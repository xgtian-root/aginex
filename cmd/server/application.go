package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	frameworkapp "github.com/xgtian-root/aginex/framework/application"
	frameworkaudit "github.com/xgtian-root/aginex/framework/audit"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/platform/database"
	"github.com/xgtian-root/aginex/internal/setup"
)

type setupApplicationInitializer struct {
	definition frameworkapp.Definition
	base       config.Config
}

var errProductionDatabaseRequired = errors.New(
	"the current production distribution requires PostgreSQL",
)

func (initializer setupApplicationInitializer) TestDatabase(
	ctx context.Context,
	databaseConfig setup.DatabaseConfig,
) error {
	candidate := initializer.base
	candidate.Database = config.Database{
		Driver: databaseConfig.Driver,
		DSN:    databaseConfig.DSN,
	}
	if err := validateDistributionDatabase(candidate); err != nil {
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

func (initializer setupApplicationInitializer) InitializeApplication(
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
	return initializeApplication(
		ctx,
		initializer.definition,
		candidate,
		frameworkapp.BootstrapOptions{
			Source:                         frameworkaudit.SourceHTTP,
			RequestID:                      metadata.RequestID,
			IPAddress:                      metadata.IPAddress,
			ReplaceAdministratorCredential: true,
		},
		reporter,
	)
}

func initializeConfiguredApplication(
	ctx context.Context,
	definition frameworkapp.Definition,
	cfg config.Config,
) (setup.Candidate, error) {
	return initializeApplication(
		ctx,
		definition,
		cfg,
		frameworkapp.BootstrapOptions{
			Source: frameworkaudit.SourceSystem,
		},
		nil,
	)
}

func initializeApplication(
	ctx context.Context,
	definition frameworkapp.Definition,
	cfg config.Config,
	bootstrapOptions frameworkapp.BootstrapOptions,
	reporter setup.ProgressReporter,
) (setup.Candidate, error) {
	if err := validateDistributionDatabase(cfg); err != nil {
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
	// Bootstrap credentials are one-time inputs. The initialized application
	// does not need them, so do not retain the administrator password in the
	// long-lived runtime configuration after access has been established.
	cfg = withoutBootstrapCredentials(cfg)

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
	resources := &applicationResources{
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

func validateDistributionDatabase(cfg config.Config) error {
	if cfg.Environment == "production" && cfg.Database.Driver != "postgres" {
		return errProductionDatabaseRequired
	}
	return nil
}

func withoutBootstrapCredentials(cfg config.Config) config.Config {
	cfg.Bootstrap = config.Bootstrap{}
	return cfg
}

type applicationLifecycle interface {
	Handler() http.Handler
	Shutdown(context.Context) error
}

type applicationResources struct {
	once        sync.Once
	application applicationLifecycle
	database    interface{ Close() error }
	err         error
}

func (resources *applicationResources) Shutdown(ctx context.Context) error {
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
