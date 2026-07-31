package postgres

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

const (
	migrationTable = "aginex_jobs_schema_versions"
	latestVersion  = int64(1)
)

var ErrSchemaNotCurrent = errors.New("postgres jobs schema is not current")

//go:embed migrations/*.sql
var migrations embed.FS

// Up applies only the opt-in PostgreSQL job module migrations.
func Up(ctx context.Context, db *sql.DB) error {
	provider, err := migrationProvider(db)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply postgres jobs migrations: %w", err)
	}
	return nil
}

// EnsureCurrent checks the module schema without creating or changing tables.
func EnsureCurrent(ctx context.Context, db *sql.DB) error {
	var tableExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name = $1
		)
	`, migrationTable).Scan(&tableExists); err != nil {
		return fmt.Errorf("check postgres jobs migration table: %w", err)
	}
	if !tableExists {
		return ErrSchemaNotCurrent
	}
	var current sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT MAX(version_id)
		FROM `+migrationTable+`
		WHERE is_applied = true
	`).Scan(&current); err != nil {
		return fmt.Errorf("read postgres jobs schema version: %w", err)
	}
	if !current.Valid || current.Int64 != latestVersion {
		return fmt.Errorf(
			"%w: current=%d latest=%d",
			ErrSchemaNotCurrent,
			current.Int64,
			latestVersion,
		)
	}
	return nil
}

func migrationProvider(db *sql.DB) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("postgres jobs migration database is required")
	}
	migrationFS, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	sessionLocker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("configure postgres jobs migration lock: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrationFS,
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
		goose.WithTableName(migrationTable),
		goose.WithSessionLocker(sessionLocker),
	)
	if err != nil {
		return nil, fmt.Errorf("configure postgres jobs migrations: %w", err)
	}
	return provider, nil
}
