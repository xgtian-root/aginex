package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/server/framework/httpx"
	frameworkidempotency "github.com/xgtian-root/aginex/server/framework/idempotency"
	postgresjobs "github.com/xgtian-root/aginex/server/framework/jobs/postgres"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/framework/ratelimit"
	"github.com/xgtian-root/aginex/server/internal/buildinfo"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"github.com/xgtian-root/aginex/server/internal/platform/migrate"
	"github.com/xgtian-root/aginex/server/internal/platform/storage"
)

const builtInReadinessTimeout = 3 * time.Second

// Ready runs the same required dependency checks as the HTTP readiness probe.
// Startup and Setup use it before publishing a newly constructed application
// so an invalid storage or module runtime cannot seal an installation.
func (a *App) Ready(ctx context.Context) error {
	if ctx == nil {
		return errors.New("application readiness context is required")
	}
	if a == nil || a.db == nil {
		return errors.New("application database is required")
	}
	sqlDB, err := a.db.DB()
	if err != nil {
		return fmt.Errorf("access readiness database: %w", err)
	}
	for _, check := range a.readinessChecks(sqlDB) {
		checkContext, cancel := context.WithTimeout(ctx, check.Timeout)
		err := check.Check(checkContext)
		cancel()
		if err != nil && check.Requirement == module.ReadinessRequired {
			return fmt.Errorf("required readiness check %q: %w", check.Name, err)
		}
	}
	return nil
}

func (a *App) live(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{
		Status:  "ok",
		Time:    time.Now().UTC(),
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
	})
}

func (a *App) ready(c *gin.Context) {
	sqlDB, err := a.db.DB()
	if err != nil {
		a.writeReadinessFailure(c, "database.connection", err)
		return
	}
	for _, check := range a.readinessChecks(sqlDB) {
		started := time.Now()
		checkContext, cancel := context.WithTimeout(
			c.Request.Context(),
			check.Timeout,
		)
		err := check.Check(checkContext)
		cancel()
		if err == nil {
			continue
		}
		log := slog.Error
		if check.Requirement == module.ReadinessOptional {
			log = slog.Warn
		}
		log(
			"Readiness check failed",
			"check", check.Name,
			"required", check.Requirement == module.ReadinessRequired,
			"duration", time.Since(started),
			"error", httpx.RedactedValue,
		)
		if check.Requirement == module.ReadinessRequired {
			a.writeReadinessFailure(c, check.Name, err)
			return
		}
	}
	if err := database.RecordPoolStats(
		a.observability,
		sqlDB,
		a.cfg.Database.Driver,
	); err != nil {
		slog.Warn(
			"Database pool metrics unavailable",
			"error", httpx.RedactedValue,
		)
	}
	c.JSON(http.StatusOK, HealthResponse{
		Status:  "ready",
		Time:    time.Now().UTC(),
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
	})
}

func (a *App) readinessChecks(
	sqlDB *sql.DB,
) []module.ReadinessCheck {
	required := func(
		name string,
		check func(context.Context) error,
	) module.ReadinessCheck {
		return module.ReadinessCheck{
			Name:        name,
			Requirement: module.ReadinessRequired,
			Timeout:     builtInReadinessTimeout,
			Check:       check,
		}
	}
	checks := []module.ReadinessCheck{
		required("database.ping", sqlDB.PingContext),
		required("database.core-migrations", func(ctx context.Context) error {
			return migrate.EnsureCurrent(
				ctx,
				sqlDB,
				a.cfg.Database.Driver,
			)
		}),
		required("database.module-migrations", func(ctx context.Context) error {
			return ensureModuleMigrationsCurrent(
				ctx,
				sqlDB,
				a.cfg.Database.Driver,
				a.registry,
			)
		}),
		required("database.rate-limit-migrations", func(ctx context.Context) error {
			return ratelimit.EnsureCurrent(
				ctx,
				sqlDB,
				a.cfg.Database.Driver,
			)
		}),
	}
	if a.cfg.Idempotency.Driver == "database" {
		checks = append(
			checks,
			required(
				"database.idempotency-migrations",
				func(ctx context.Context) error {
					return frameworkidempotency.EnsureCurrent(
						ctx,
						sqlDB,
						a.cfg.Database.Driver,
					)
				},
			),
		)
	}
	if a.cfg.Jobs.Driver == "postgres" {
		checks = append(
			checks,
			required(
				"database.jobs-migrations",
				func(ctx context.Context) error {
					return postgresjobs.EnsureCurrent(ctx, sqlDB)
				},
			),
		)
	}
	checks = append(
		checks,
		required("storage", func(ctx context.Context) error {
			checker, ok := a.store.(storage.ReadinessChecker)
			if !ok {
				return errors.New(
					"configured storage provider has no readiness check",
				)
			}
			return checker.CheckReadiness(ctx)
		}),
	)
	return append(checks, a.registry.ReadinessChecks()...)
}

func (a *App) writeReadinessFailure(
	c *gin.Context,
	_ string,
	_ error,
) {
	writeProblem(
		c,
		http.StatusServiceUnavailable,
		"Service not ready",
		"A required runtime dependency is unavailable.",
	)
}
