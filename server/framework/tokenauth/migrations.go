package tokenauth

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
	// MigrationTableName isolates this optional module from the application's
	// core Goose history.
	MigrationTableName = "aginex_tokenauth_migrations"
	latestMigration    = int64(2)
)

var ErrSchemaNotCurrent = errors.New("token authentication schema is not current")

type MigrationStatus struct {
	Current int64
	Latest  int64
	Pending int
}

func (status MigrationStatus) IsCurrent() bool {
	return status.Current == status.Latest && status.Pending == 0
}

//go:embed migrations/*/*.sql
var migrationFiles embed.FS

// NewMigrationProvider returns the isolated Goose provider. Applications call
// it only from their explicit migrate process.
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
		return nil, fmt.Errorf("open %s token authentication migrations: %w", dialect, err)
	}
	options := []goose.ProviderOption{
		goose.WithTableName(MigrationTableName),
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	}
	if dialect == "postgres" {
		sessionLocker, lockerErr := lock.NewPostgresSessionLocker()
		if lockerErr != nil {
			return nil, fmt.Errorf("configure postgres token authentication migration lock: %w", lockerErr)
		}
		options = append(options, goose.WithSessionLocker(sessionLocker))
	}
	provider, err := goose.NewProvider(gooseDialect, db, source, options...)
	if err != nil {
		return nil, fmt.Errorf("configure %s token authentication migrations: %w", dialect, err)
	}
	return provider, nil
}

// Status reads the optional schema state without creating tables.
func Status(ctx context.Context, db *sql.DB, dialect string) (MigrationStatus, error) {
	if ctx == nil {
		return MigrationStatus{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if db == nil {
		return MigrationStatus{}, ErrDatabaseRequired
	}
	dialect = strings.ToLower(strings.TrimSpace(dialect))
	var existsQuery string
	switch dialect {
	case "sqlite":
		existsQuery = `
			SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`
	case "postgres":
		existsQuery = `
			SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1
		`
	case "mysql":
		existsQuery = `
			SELECT COUNT(*) FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?
		`
	default:
		return MigrationStatus{}, fmt.Errorf("%w: %q", ErrUnsupportedDialect, dialect)
	}
	var count int
	if err := db.QueryRowContext(ctx, existsQuery, MigrationTableName).Scan(&count); err != nil {
		return MigrationStatus{}, fmt.Errorf("check %s token authentication migration table: %w", dialect, err)
	}
	status := MigrationStatus{
		Latest:  latestMigration,
		Pending: int(latestMigration),
	}
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
		return MigrationStatus{}, fmt.Errorf("read %s token authentication migration version: %w", dialect, err)
	}
	if current.Valid {
		status.Current = current.Int64
	}
	status.Pending = int(status.Latest - status.Current)
	return status, nil
}

// EnsureCurrent verifies the optional schema without mutating it.
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
