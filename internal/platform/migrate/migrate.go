package migrate

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
	"github.com/xgtian-root/aginex/framework/ratelimit"
)

//go:embed migrations/*/*.sql
var migrationFS embed.FS

var ErrSchemaNotCurrent = errors.New("database schema is not current")

type StatusInfo struct {
	Driver    string
	Current   int64
	Latest    int64
	Applied   int
	Pending   int
	RateLimit ratelimit.MigrationStatus
}

func (s StatusInfo) IsCurrent() bool {
	return s.Current == s.Latest && s.Pending == 0 && s.RateLimit.IsCurrent()
}

func (s StatusInfo) State() string {
	switch {
	case s.IsCurrent():
		return "current"
	case s.Current > s.Latest:
		return "ahead"
	default:
		return "pending"
	}
}

type SchemaVersionError struct {
	Current          int64
	Latest           int64
	Pending          int
	RateLimitCurrent int64
	RateLimitLatest  int64
	RateLimitPending int
}

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf(
		"%v: current=%d latest=%d pending=%d rate_limit_current=%d rate_limit_latest=%d rate_limit_pending=%d",
		ErrSchemaNotCurrent,
		e.Current,
		e.Latest,
		e.Pending,
		e.RateLimitCurrent,
		e.RateLimitLatest,
		e.RateLimitPending,
	)
}

func (e *SchemaVersionError) Unwrap() error {
	return ErrSchemaNotCurrent
}

func Up(db *sql.DB, driver string) error {
	return UpContext(context.Background(), db, driver)
}

func UpContext(ctx context.Context, db *sql.DB, driver string) error {
	provider, err := newProvider(db, driver)
	if err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply %s migrations: %w", driver, err)
	}
	rateLimitProvider, err := ratelimit.NewMigrationProvider(db, driver)
	if err != nil {
		return fmt.Errorf("configure %s rate limit migrations: %w", driver, err)
	}
	if _, err := rateLimitProvider.Up(ctx); err != nil {
		return fmt.Errorf("apply %s rate limit migrations: %w", driver, err)
	}
	return nil
}

func DownToZero(db *sql.DB, driver string) error {
	rateLimitProvider, err := ratelimit.NewMigrationProvider(db, driver)
	if err != nil {
		return err
	}
	if _, err := rateLimitProvider.DownTo(context.Background(), 0); err != nil {
		return fmt.Errorf("roll back %s rate limit migrations: %w", driver, err)
	}
	provider, err := newProvider(db, driver)
	if err != nil {
		return err
	}
	if _, err := provider.DownTo(context.Background(), 0); err != nil {
		return fmt.Errorf("roll back %s migrations: %w", driver, err)
	}
	return nil
}

func Latest(driver string) (int64, error) {
	versions, err := migrationVersions(driver)
	if err != nil {
		return 0, err
	}
	return versions[len(versions)-1], nil
}

func Current(ctx context.Context, db *sql.DB, driver string) (int64, error) {
	versions, err := appliedVersions(ctx, db, driver)
	if err != nil {
		return 0, err
	}
	var current int64
	for _, version := range versions {
		if version > current {
			current = version
		}
	}
	return current, nil
}

func Status(ctx context.Context, db *sql.DB, driver string) (StatusInfo, error) {
	knownVersions, err := migrationVersions(driver)
	if err != nil {
		return StatusInfo{}, err
	}
	appliedVersions, err := appliedVersions(ctx, db, driver)
	if err != nil {
		return StatusInfo{}, err
	}
	applied := make(map[int64]struct{}, len(appliedVersions))
	var current int64
	for _, version := range appliedVersions {
		applied[version] = struct{}{}
		if version > current {
			current = version
		}
	}
	status := StatusInfo{
		Driver:  driver,
		Current: current,
		Latest:  knownVersions[len(knownVersions)-1],
	}
	for _, version := range knownVersions {
		if _, ok := applied[version]; ok {
			status.Applied++
		} else {
			status.Pending++
		}
	}
	status.RateLimit, err = ratelimit.Status(ctx, db, driver)
	if err != nil {
		return StatusInfo{}, err
	}
	return status, nil
}

func EnsureCurrent(ctx context.Context, db *sql.DB, driver string) error {
	status, err := Status(ctx, db, driver)
	if err != nil {
		return err
	}
	if !status.IsCurrent() {
		return &SchemaVersionError{
			Current:          status.Current,
			Latest:           status.Latest,
			Pending:          status.Pending,
			RateLimitCurrent: status.RateLimit.Current,
			RateLimitLatest:  status.RateLimit.Latest,
			RateLimitPending: status.RateLimit.Pending,
		}
	}
	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB, driver string) ([]int64, error) {
	exists, err := versionTableExists(ctx, db, driver)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx, `
		SELECT applied.version_id
		FROM goose_db_version AS applied
		WHERE applied.is_applied = true
		  AND applied.id = (
			SELECT MAX(latest.id)
			FROM goose_db_version AS latest
			WHERE latest.version_id = applied.version_id
		  )
		ORDER BY applied.version_id
	`)
	if err != nil {
		return nil, fmt.Errorf("read %s migration versions: %w", driver, err)
	}
	defer rows.Close()
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("scan %s migration version: %w", driver, err)
		}
		if version > 0 {
			versions = append(versions, version)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s migration versions: %w", driver, err)
	}
	return versions, nil
}

func versionTableExists(ctx context.Context, db *sql.DB, driver string) (bool, error) {
	var query string
	switch driver {
	case "sqlite":
		query = `
			SELECT COUNT(*)
			FROM sqlite_master
			WHERE type = 'table' AND name = 'goose_db_version'
		`
	case "postgres":
		query = `
			SELECT COUNT(*)
			FROM information_schema.tables
			WHERE table_schema = current_schema() AND table_name = 'goose_db_version'
		`
	case "mysql":
		query = `
			SELECT COUNT(*)
			FROM information_schema.tables
			WHERE table_schema = DATABASE() AND table_name = 'goose_db_version'
		`
	default:
		return false, fmt.Errorf("unsupported migration driver %q", driver)
	}
	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return false, fmt.Errorf("check %s migration version table: %w", driver, err)
	}
	return count > 0, nil
}

func newProvider(db *sql.DB, driver string) (*goose.Provider, error) {
	gooseDialect, err := dialect(driver)
	if err != nil {
		return nil, err
	}
	driverFS, err := fs.Sub(migrationFS, "migrations/"+driver)
	if err != nil {
		return nil, fmt.Errorf("open %s migrations: %w", driver, err)
	}
	options := []goose.ProviderOption{
		goose.WithDisableGlobalRegistry(true),
		goose.WithLogger(goose.NopLogger()),
	}
	sessionLocker, err := migrationSessionLocker(driver)
	if err != nil {
		return nil, err
	}
	if sessionLocker != nil {
		options = append(options, goose.WithSessionLocker(sessionLocker))
	}
	provider, err := goose.NewProvider(gooseDialect, db, driverFS, options...)
	if err != nil {
		return nil, fmt.Errorf("configure %s migrations: %w", driver, err)
	}
	return provider, nil
}

// migrationSessionLocker protects explicit PostgreSQL migration runs across
// processes. Goose runs SQL migrations on the same dedicated connection that
// owns this advisory lock and releases it with a detached context.
func migrationSessionLocker(driver string) (lock.SessionLocker, error) {
	switch driver {
	case "postgres":
		sessionLocker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			return nil, fmt.Errorf("configure postgres migration lock: %w", err)
		}
		return sessionLocker, nil
	case "sqlite", "mysql":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported migration driver %q", driver)
	}
}

func migrationVersions(driver string) ([]int64, error) {
	if _, err := dialect(driver); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(migrationFS, "migrations/"+driver)
	if err != nil {
		return nil, fmt.Errorf("read %s migrations: %w", driver, err)
	}
	versions := make([]int64, 0, len(entries))
	seen := make(map[int64]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		versionText, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			return nil, fmt.Errorf("invalid %s migration filename %q", driver, entry.Name())
		}
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid %s migration filename %q", driver, entry.Name())
		}
		if previous, exists := seen[version]; exists {
			return nil, fmt.Errorf(
				"duplicate %s migration version %d in %q and %q",
				driver,
				version,
				previous,
				entry.Name(),
			)
		}
		seen[version] = entry.Name()
		versions = append(versions, version)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no %s migrations found", driver)
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i] < versions[j]
	})
	return versions, nil
}

func dialect(driver string) (goose.Dialect, error) {
	switch driver {
	case "sqlite":
		return goose.DialectSQLite3, nil
	case "postgres":
		return goose.DialectPostgres, nil
	case "mysql":
		return goose.DialectMySQL, nil
	default:
		return goose.DialectCustom, fmt.Errorf("unsupported migration driver %q", driver)
	}
}
