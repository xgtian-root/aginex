package idempotency

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

const (
	// MigrationTableName isolates the optional module's history from the
	// application's core Goose versions.
	MigrationTableName = "aginex_idempotency_migrations"
	latestMigration    = int64(1)
)

// ErrSchemaNotCurrent means the module has not been explicitly installed or
// its schema is behind the package.
var ErrSchemaNotCurrent = errors.New("idempotency schema is not current")

// MigrationStatus describes the isolated module schema state.
type MigrationStatus struct {
	Current int64
	Latest  int64
	Pending int
}

// IsCurrent reports whether every embedded migration is applied.
func (status MigrationStatus) IsCurrent() bool {
	return status.Current == status.Latest && status.Pending == 0
}

//go:embed migrations/*/*.sql
var migrationFiles embed.FS

// NewMigrationProvider returns an isolated Goose provider. It must only be
// executed by an explicit migrate process.
func NewMigrationProvider(db *sql.DB, dialect string) (*goose.Provider, error) {
	if db == nil {
		return nil, ErrDatabaseRequired
	}
	dialect = strings.ToLower(strings.TrimSpace(dialect))
	var (
		gooseDialect goose.Dialect
		directory    string
	)
	switch dialect {
	case "sqlite":
		gooseDialect = goose.DialectSQLite3
		directory = "sqlite"
	case "postgres":
		gooseDialect = goose.DialectPostgres
		directory = "postgres"
	case "mysql":
		gooseDialect = goose.DialectMySQL
		directory = "mysql"
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDialect, dialect)
	}
	source, err := fs.Sub(migrationFiles, "migrations/"+directory)
	if err != nil {
		return nil, fmt.Errorf("open %s idempotency migrations: %w", dialect, err)
	}
	options, err := migrationOptions(dialect)
	if err != nil {
		return nil, err
	}
	provider, err := goose.NewProvider(gooseDialect, db, source, options...)
	if err != nil {
		return nil, fmt.Errorf("configure %s idempotency migrations: %w", dialect, err)
	}
	return provider, nil
}

// Status reads migration state without creating the history or state tables.
func Status(ctx context.Context, db *sql.DB, dialect string) (MigrationStatus, error) {
	if db == nil {
		return MigrationStatus{}, ErrDatabaseRequired
	}
	if ctx == nil {
		return MigrationStatus{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	dialect = strings.ToLower(strings.TrimSpace(dialect))
	var existsQuery string
	switch dialect {
	case "sqlite":
		existsQuery = `
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`
	case "postgres":
		existsQuery = `
			SELECT COUNT(*)
			FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1
		`
	case "mysql":
		existsQuery = `
			SELECT COUNT(*)
			FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?
		`
	default:
		return MigrationStatus{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, dialect)
	}

	var count int
	if err := db.QueryRowContext(ctx, existsQuery, MigrationTableName).Scan(&count); err != nil {
		return MigrationStatus{}, fmt.Errorf(
			"check %s idempotency migration table: %w",
			dialect,
			err,
		)
	}
	status := MigrationStatus{Latest: latestMigration, Pending: 1}
	if count == 0 {
		return status, nil
	}
	var current sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT MAX(applied.version_id)
		FROM `+MigrationTableName+` AS applied
		WHERE applied.is_applied = true
		  AND applied.id = (
			SELECT MAX(latest.id)
			FROM `+MigrationTableName+` AS latest
			WHERE latest.version_id = applied.version_id
		  )
	`).Scan(&current); err != nil {
		return MigrationStatus{}, fmt.Errorf(
			"read %s idempotency migration version: %w",
			dialect,
			err,
		)
	}
	if current.Valid {
		status.Current = current.Int64
	}
	if status.Current == status.Latest {
		status.Pending = 0
	}
	return status, nil
}

// EnsureCurrent checks schema state without mutating it.
func EnsureCurrent(ctx context.Context, db *sql.DB, dialect string) error {
	status, err := Status(ctx, db, dialect)
	if err != nil {
		return err
	}
	if !status.IsCurrent() {
		return fmt.Errorf(
			"%w: current=%d latest=%d pending=%d",
			ErrSchemaNotCurrent,
			status.Current,
			status.Latest,
			status.Pending,
		)
	}
	return nil
}

func migrationOptions(dialect string) ([]goose.ProviderOption, error) {
	options := []goose.ProviderOption{
		goose.WithTableName(MigrationTableName),
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	}
	if dialect != "postgres" {
		return options, nil
	}
	sessionLocker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("configure postgres idempotency migration lock: %w", err)
	}
	return append(options, goose.WithSessionLocker(sessionLocker)), nil
}
