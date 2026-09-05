package app

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/xgtian-root/aginex/backend/framework/idempotency"
	postgresjobs "github.com/xgtian-root/aginex/backend/framework/jobs/postgres"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/internal/config"
	coremigrate "github.com/xgtian-root/aginex/backend/internal/platform/migrate"
)

// MigrateUp is the explicit schema-mutating entry point used by derived
// applications. It applies core, configured opt-in, and application-module
// migrations; API and worker constructors never call it.
func MigrateUp(
	ctx context.Context,
	db *sql.DB,
	cfg config.Config,
	applicationModules ...module.Module,
) error {
	cfg = config.WithDefaults(cfg)
	if err := coremigrate.UpContext(ctx, db, cfg.Database.Driver); err != nil {
		return err
	}
	switch cfg.Idempotency.Driver {
	case "disabled":
	case "database":
		provider, err := idempotency.NewMigrationProvider(
			db,
			cfg.Database.Driver,
		)
		if err != nil {
			return fmt.Errorf("configure idempotency migrations: %w", err)
		}
		if _, err := provider.Up(ctx); err != nil {
			return fmt.Errorf(
				"apply %s idempotency migrations: %w",
				cfg.Database.Driver,
				err,
			)
		}
	default:
		return fmt.Errorf(
			"unsupported idempotency migration driver %q",
			cfg.Idempotency.Driver,
		)
	}
	switch cfg.Jobs.Driver {
	case "disabled":
	case "postgres":
		if cfg.Database.Driver != "postgres" {
			return fmt.Errorf(
				"postgres jobs migrations require the postgres database driver",
			)
		}
		if err := postgresjobs.Up(ctx, db); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"unsupported jobs migration driver %q",
			cfg.Jobs.Driver,
		)
	}
	return MigrateModulesUp(
		ctx,
		db,
		cfg.Database.Driver,
		applicationModules...,
	)
}

// MigrateModulesUp explicitly applies every migration bundle registered by
// the supplied compiled-in application modules. Derived applications call this
// from their migrate process after Aginex core migrations; API and worker
// startup must use the read-only checks instead.
func MigrateModulesUp(
	ctx context.Context,
	db *sql.DB,
	driver string,
	applicationModules ...module.Module,
) error {
	registry, err := applicationModuleRegistry(applicationModules...)
	if err != nil {
		return err
	}
	dialect, err := moduleDialect(driver)
	if err != nil {
		return err
	}
	if err := registry.MigrateUp(ctx, db, dialect); err != nil {
		return fmt.Errorf("apply application module migrations: %w", err)
	}
	return nil
}

// EnsureModulesCurrent performs the read-only application-module migration
// check used by runtime startup, readiness, and the public composition root.
func EnsureModulesCurrent(
	ctx context.Context,
	db *sql.DB,
	driver string,
	applicationModules ...module.Module,
) error {
	registry, err := applicationModuleRegistry(applicationModules...)
	if err != nil {
		return err
	}
	return ensureModuleMigrationsCurrent(ctx, db, driver, registry)
}

func ensureModuleMigrationsCurrent(
	ctx context.Context,
	db *sql.DB,
	driver string,
	registry *module.Registry,
) error {
	dialect, err := moduleDialect(driver)
	if err != nil {
		return err
	}
	if err := registry.EnsureMigrationsCurrent(ctx, db, dialect); err != nil {
		return fmt.Errorf("check application module migrations: %w", err)
	}
	return nil
}

func applicationModuleRegistry(
	applicationModules ...module.Module,
) (*module.Registry, error) {
	registry := module.NewRegistry()
	if err := registry.RegisterModules(applicationModules...); err != nil {
		return nil, fmt.Errorf("compose application modules: %w", err)
	}
	return registry, nil
}

func moduleDialect(driver string) (module.Dialect, error) {
	dialect := module.Dialect(driver)
	switch dialect {
	case module.DialectSQLite, module.DialectPostgreSQL, module.DialectMySQL:
		return dialect, nil
	default:
		return "", fmt.Errorf("unsupported application module migration dialect %q", driver)
	}
}
