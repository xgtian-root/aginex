package app

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/xgtian-root/aginex/framework/module"
)

type bundledMigration string

const (
	bundledFiles   bundledMigration = "files"
	bundledStarter bundledMigration = "starter"
)

type bundledMigrationSpec struct {
	name         string
	directory    string
	historyTable string
	table        string
	columns      []string
}

//go:embed migrations/*/*/*.sql
var bundledMigrationFiles embed.FS

var errBundledSchemaNotCurrent = errors.New(
	"bundled module database schema is not current",
)

func bundledMigrationBundle(
	migration bundledMigration,
) (module.MigrationBundle, error) {
	spec, err := bundledMigrationSpecification(migration)
	if err != nil {
		return module.MigrationBundle{}, err
	}
	executor := module.MigrationExecutor{
		Up: func(
			ctx context.Context,
			db *sql.DB,
			dialect module.Dialect,
		) error {
			provider, err := newBundledMigrationProvider(
				ctx,
				db,
				dialect,
				spec,
			)
			if err != nil {
				return err
			}
			if _, err := provider.Up(ctx); err != nil {
				return fmt.Errorf(
					"apply %s migrations for %s: %w",
					dialect,
					spec.name,
					err,
				)
			}
			if err := ensureBundledMigrationCurrent(
				ctx,
				db,
				dialect,
				spec,
			); err != nil {
				return fmt.Errorf(
					"verify %s migrations for %s: %w",
					dialect,
					spec.name,
					err,
				)
			}
			return nil
		},
		EnsureCurrent: func(
			ctx context.Context,
			db *sql.DB,
			dialect module.Dialect,
		) error {
			return ensureBundledMigrationCurrent(
				ctx,
				db,
				dialect,
				spec,
			)
		},
	}
	return module.NewExecutableMigrationBundle(
		spec.name,
		executor,
		module.MigrationSource{
			Dialect:   module.DialectSQLite,
			Directory: "migrations/" + spec.directory + "/sqlite",
		},
		module.MigrationSource{
			Dialect:   module.DialectPostgreSQL,
			Directory: "migrations/" + spec.directory + "/postgres",
		},
		module.MigrationSource{
			Dialect:   module.DialectMySQL,
			Directory: "migrations/" + spec.directory + "/mysql",
		},
	)
}

func bundledMigrationSpecification(
	migration bundledMigration,
) (bundledMigrationSpec, error) {
	switch migration {
	case bundledFiles:
		return bundledMigrationSpec{
			name:         "files",
			directory:    "files",
			historyTable: "aginex_files_migrations",
			table:        "file_objects",
			columns: []string{
				"id",
				"provider",
				"bucket",
				"object_key",
				"original_name",
				"content_type",
				"size",
				"etag",
				"sha256",
				"width",
				"height",
				"owner_id",
				"visibility",
				"status",
				"created_at",
				"updated_at",
				"deleted_at",
			},
		}, nil
	case bundledStarter:
		return bundledMigrationSpec{
			name:         "starter-example",
			directory:    "starter",
			historyTable: "aginex_starter_migrations",
			table:        "products",
			columns: []string{
				"id",
				"name",
				"sku",
				"price_cents",
				"status",
				"created_at",
				"updated_at",
			},
		}, nil
	default:
		return bundledMigrationSpec{}, fmt.Errorf(
			"unsupported bundled migration %q",
			migration,
		)
	}
}

func newBundledMigrationProvider(
	ctx context.Context,
	db *sql.DB,
	dialect module.Dialect,
	spec bundledMigrationSpec,
) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("bundled migrations require a database")
	}
	gooseDialect, directory, err := bundledGooseDialect(dialect, spec)
	if err != nil {
		return nil, err
	}
	directory, err = bundledMigrationSourceDirectory(
		ctx,
		db,
		dialect,
		spec,
		directory,
	)
	if err != nil {
		return nil, err
	}
	source, err := fs.Sub(bundledMigrationFiles, directory)
	if err != nil {
		return nil, fmt.Errorf(
			"open %s migrations for %s: %w",
			dialect,
			spec.name,
			err,
		)
	}
	options := []goose.ProviderOption{
		goose.WithTableName(spec.historyTable),
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	}
	if dialect == module.DialectPostgreSQL {
		sessionLocker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			return nil, fmt.Errorf(
				"configure postgres migration lock for %s: %w",
				spec.name,
				err,
			)
		}
		options = append(options, goose.WithSessionLocker(sessionLocker))
	}
	provider, err := goose.NewProvider(
		gooseDialect,
		db,
		source,
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf(
			"configure %s migrations for %s: %w",
			dialect,
			spec.name,
			err,
		)
	}
	return provider, nil
}

func ensureBundledMigrationCurrent(
	ctx context.Context,
	db *sql.DB,
	dialect module.Dialect,
	spec bundledMigrationSpec,
) error {
	if db == nil {
		return errors.New("bundled migrations require a database")
	}
	latest, err := latestBundledMigrationVersion(dialect, spec)
	if err != nil {
		return err
	}
	exists, err := bundledHistoryTableExists(
		ctx,
		db,
		dialect,
		spec.historyTable,
	)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf(
			"%w: bundle=%s current=0 latest=%d pending=1",
			errBundledSchemaNotCurrent,
			spec.name,
			latest,
		)
	}
	var current sql.NullInt64
	if err := db.QueryRowContext(ctx, `
		SELECT MAX(applied.version_id)
		FROM `+spec.historyTable+` AS applied
		WHERE applied.is_applied = true
		  AND applied.id = (
			SELECT MAX(latest.id)
			FROM `+spec.historyTable+` AS latest
			WHERE latest.version_id = applied.version_id
		  )
	`).Scan(&current); err != nil {
		return fmt.Errorf(
			"read %s migration version for %s: %w",
			dialect,
			spec.name,
			err,
		)
	}
	if !current.Valid || current.Int64 != latest {
		currentVersion := int64(0)
		if current.Valid {
			currentVersion = current.Int64
		}
		return fmt.Errorf(
			"%w: bundle=%s current=%d latest=%d pending=1",
			errBundledSchemaNotCurrent,
			spec.name,
			currentVersion,
			latest,
		)
	}
	columns, err := bundledTableColumns(ctx, db, dialect, spec.table)
	if err != nil {
		return err
	}
	for _, column := range spec.columns {
		if _, exists := columns[column]; !exists {
			return fmt.Errorf(
				"%w: bundle=%s table=%s missing_column=%s",
				errBundledSchemaNotCurrent,
				spec.name,
				spec.table,
				column,
			)
		}
	}
	return nil
}

func bundledMigrationSourceDirectory(
	ctx context.Context,
	db *sql.DB,
	dialect module.Dialect,
	spec bundledMigrationSpec,
	defaultDirectory string,
) (string, error) {
	if spec.directory != "files" {
		return defaultDirectory, nil
	}
	exists, err := bundledTableExists(
		ctx,
		db,
		dialect,
		spec.table,
	)
	if err != nil {
		return "", err
	}
	if !exists {
		return defaultDirectory, nil
	}
	columns, err := bundledTableColumns(
		ctx,
		db,
		dialect,
		spec.table,
	)
	if err != nil {
		return "", err
	}
	verificationColumns := []string{
		"sha256",
		"width",
		"height",
		"deleted_at",
	}
	missing := make([]string, 0, len(verificationColumns))
	for _, column := range verificationColumns {
		if _, ok := columns[column]; !ok {
			missing = append(missing, column)
		}
	}
	if len(missing) == 0 {
		return defaultDirectory, nil
	}
	if len(missing) == len(verificationColumns) {
		return "migrations/files_legacy/" + string(dialect), nil
	}
	return "", fmt.Errorf(
		"cannot adopt partially upgraded %s table: missing columns %s",
		spec.table,
		strings.Join(missing, ", "),
	)
}

func bundledHistoryTableExists(
	ctx context.Context,
	db *sql.DB,
	dialect module.Dialect,
	table string,
) (bool, error) {
	return bundledTableExists(ctx, db, dialect, table)
}

func bundledTableExists(
	ctx context.Context,
	db *sql.DB,
	dialect module.Dialect,
	table string,
) (bool, error) {
	var query string
	switch dialect {
	case module.DialectSQLite:
		query = `
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'table' AND name = ?
		`
	case module.DialectPostgreSQL:
		query = `
			SELECT COUNT(*)
			FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = $1
		`
	case module.DialectMySQL:
		query = `
			SELECT COUNT(*)
			FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = ?
		`
	default:
		return false, fmt.Errorf(
			"unsupported bundled migration dialect %q",
			dialect,
		)
	}
	var count int
	if err := db.QueryRowContext(ctx, query, table).Scan(&count); err != nil {
		return false, fmt.Errorf(
			"check %s table %s: %w",
			dialect,
			table,
			err,
		)
	}
	return count > 0, nil
}

func bundledTableColumns(
	ctx context.Context,
	db *sql.DB,
	dialect module.Dialect,
	table string,
) (map[string]struct{}, error) {
	var (
		query string
		args  []any
	)
	switch dialect {
	case module.DialectSQLite:
		switch table {
		case "file_objects":
			query = "SELECT name FROM pragma_table_info('file_objects')"
		case "products":
			query = "SELECT name FROM pragma_table_info('products')"
		default:
			return nil, fmt.Errorf(
				"unsupported bundled migration table %q",
				table,
			)
		}
	case module.DialectPostgreSQL:
		query = `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1
		`
		args = append(args, table)
	case module.DialectMySQL:
		query = `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_schema = DATABASE() AND table_name = ?
		`
		args = append(args, table)
	default:
		return nil, fmt.Errorf(
			"unsupported bundled migration dialect %q",
			dialect,
		)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf(
			"read %s columns for %s: %w",
			dialect,
			table,
			err,
		)
	}
	defer rows.Close()
	columns := make(map[string]struct{})
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, fmt.Errorf(
				"scan %s column for %s: %w",
				dialect,
				table,
				err,
			)
		}
		columns[column] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"read %s columns for %s: %w",
			dialect,
			table,
			err,
		)
	}
	return columns, nil
}

func latestBundledMigrationVersion(
	dialect module.Dialect,
	spec bundledMigrationSpec,
) (int64, error) {
	_, directory, err := bundledGooseDialect(dialect, spec)
	if err != nil {
		return 0, err
	}
	entries, err := fs.ReadDir(bundledMigrationFiles, directory)
	if err != nil {
		return 0, fmt.Errorf(
			"read %s migrations for %s: %w",
			dialect,
			spec.name,
			err,
		)
	}
	var latest int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return 0, fmt.Errorf(
				"invalid %s migration filename %q for %s",
				dialect,
				entry.Name(),
				spec.name,
			)
		}
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil || version < 1 {
			return 0, fmt.Errorf(
				"invalid %s migration filename %q for %s",
				dialect,
				entry.Name(),
				spec.name,
			)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf(
			"no %s migrations found for %s",
			dialect,
			spec.name,
		)
	}
	return latest, nil
}

func bundledGooseDialect(
	dialect module.Dialect,
	spec bundledMigrationSpec,
) (goose.Dialect, string, error) {
	directory := "migrations/" + spec.directory + "/" + string(dialect)
	switch dialect {
	case module.DialectSQLite:
		return goose.DialectSQLite3, directory, nil
	case module.DialectPostgreSQL:
		return goose.DialectPostgres, directory, nil
	case module.DialectMySQL:
		return goose.DialectMySQL, directory, nil
	default:
		return goose.DialectCustom, "", fmt.Errorf(
			"unsupported bundled migration dialect %q",
			dialect,
		)
	}
}
