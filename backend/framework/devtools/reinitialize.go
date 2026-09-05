package devtools

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/platform/database"
)

const reinitializeTimeout = 30 * time.Second

var safeSQLNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type reinitializeDependencies struct {
	now         func() time.Time
	environment func() string
}

type reinitializeInstallation struct {
	Version  int
	Database config.InstallationDatabase
}

type reinitializePlan struct {
	configPath      string
	configIdentity  os.FileInfo
	backupDirectory string
	confirmation    string
	databaseTarget  string
	databaseBackup  string
	database        config.Database
}

type reinitializeManifest struct {
	CreatedAt         time.Time `json:"createdAt"`
	ConfigurationPath string    `json:"configurationPath"`
	DatabaseDriver    string    `json:"databaseDriver"`
	DatabaseTarget    string    `json:"databaseTarget"`
	DatabaseBackup    string    `json:"databaseBackup"`
}

type postgresRelation struct {
	name  string
	kind  string
	owner string
}

type postgresRoutine struct {
	name              string
	identityArguments string
	kind              string
	owner             string
}

func newReinitializeCommand(dependencies reinitializeDependencies) *cobra.Command {
	var configPath string
	var confirmation string
	command := &cobra.Command{
		Use:   "reinitialize",
		Short: "Archive stale pre-release local state and return to browser Setup",
		Long: "Archive stale pre-release local configuration and database state before returning to browser Setup. " +
			"Without --confirm this command is a non-mutating dry run that prints the exact sanitized target required for execution.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			environment := "development"
			if dependencies.environment != nil {
				environment = strings.ToLower(strings.TrimSpace(dependencies.environment()))
			} else if value := strings.TrimSpace(os.Getenv("AGINEX_ENV")); value != "" {
				environment = strings.ToLower(value)
			}
			if environment != "development" && environment != "test" {
				return fmt.Errorf("local reinitialization is disabled in %q environment", environment)
			}
			now := time.Now().UTC()
			if dependencies.now != nil {
				now = dependencies.now().UTC()
			}
			plan, err := buildReinitializePlan(configPath, now)
			if err != nil {
				return err
			}
			if strings.TrimSpace(confirmation) == "" {
				printReinitializePlan(cmd, plan)
				return nil
			}
			if confirmation != plan.confirmation {
				return fmt.Errorf(
					"confirmation does not match target; run without --confirm to inspect the exact value",
				)
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), reinitializeTimeout)
			defer cancel()
			if err := executeReinitializePlan(ctx, plan, now); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "Reinitialization complete.")
			fmt.Fprintf(cmd.OutOrStdout(), "Backup: %s\n", plan.backupDirectory)
			fmt.Fprintln(cmd.OutOrStdout(), "Run `aginex dev` and complete browser Setup with a new administrator.")
			return nil
		},
	}
	command.Flags().StringVar(
		&configPath,
		"config",
		config.ConfigFilePath(),
		"installation configuration to archive",
	)
	command.Flags().StringVar(
		&confirmation,
		"confirm",
		"",
		"exact database target printed by the dry run",
	)
	return command
}

func printReinitializePlan(command *cobra.Command, plan reinitializePlan) {
	fmt.Fprintln(command.OutOrStdout(), "Local pre-release reinitialization plan")
	fmt.Fprintf(command.OutOrStdout(), "Configuration: %s\n", plan.configPath)
	fmt.Fprintf(command.OutOrStdout(), "Database: %s\n", plan.databaseTarget)
	fmt.Fprintf(command.OutOrStdout(), "Backup: %s\n", plan.backupDirectory)
	fmt.Fprintln(command.OutOrStdout(), "Stop the API and worker before continuing. No changes were made.")
	fmt.Fprintln(command.OutOrStdout(), "To execute this recoverable reset, run:")
	fmt.Fprintf(
		command.OutOrStdout(),
		"  aginex dev reinitialize --config %s --confirm %s\n",
		strconv.Quote(plan.configPath),
		strconv.Quote(plan.confirmation),
	)
}

func buildReinitializePlan(path string, now time.Time) (reinitializePlan, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return reinitializePlan{}, errors.New("installation configuration path is required")
	}
	installation, identity, err := inspectReinitializeInstallation(path)
	if err != nil {
		return reinitializePlan{}, err
	}
	if installation.Database.Source != config.DatabaseSourceManaged {
		return reinitializePlan{}, errors.New(
			"local reinitialization requires a configuration-managed database; environment-managed databases are never reset",
		)
	}
	databaseConfig := config.Database{
		Driver: strings.ToLower(strings.TrimSpace(installation.Database.Driver)),
		DSN:    strings.TrimSpace(installation.Database.DSN),
	}
	stamp := now.UTC().Format("20060102T150405Z")
	backupDirectory := filepath.Join(filepath.Dir(path), "reinitialize-backups", stamp)
	plan := reinitializePlan{
		configPath:      path,
		configIdentity:  identity,
		backupDirectory: backupDirectory,
		database:        databaseConfig,
	}
	switch databaseConfig.Driver {
	case "sqlite":
		databasePath, pathErr := localSQLitePath(databaseConfig.DSN)
		if pathErr != nil {
			return reinitializePlan{}, pathErr
		}
		plan.database.DSN = databasePath
		plan.confirmation = "sqlite:" + databasePath
		plan.databaseTarget = plan.confirmation
		plan.databaseBackup = filepath.Join(backupDirectory, "database.sqlite")
	case "postgres":
		parsed, parseErr := pgx.ParseConfig(databaseConfig.DSN)
		if parseErr != nil {
			return reinitializePlan{}, errors.New("parse PostgreSQL target for local reinitialization")
		}
		if !localDatabaseHost(parsed.Host) || strings.TrimSpace(parsed.Database) == "" ||
			reservedPostgresDatabase(parsed.Database) {
			return reinitializePlan{}, errors.New("local reinitialization refuses a non-local PostgreSQL target")
		}
		host := parsed.Host
		if strings.HasPrefix(host, "/") {
			host = "local-socket"
		}
		plan.confirmation = fmt.Sprintf("postgres:%s@%s:%d", parsed.Database, host, parsed.Port)
		plan.databaseTarget = plan.confirmation
		plan.databaseBackup = postgresBackupSchema(stamp)
	case "mysql":
		parsed, parseErr := mysqldriver.ParseDSN(databaseConfig.DSN)
		if parseErr != nil {
			return reinitializePlan{}, errors.New("parse MySQL target for local reinitialization")
		}
		host, local := localMySQLAddress(parsed.Net, parsed.Addr)
		if !local || strings.TrimSpace(parsed.DBName) == "" ||
			reservedMySQLDatabase(parsed.DBName) {
			return reinitializePlan{}, errors.New("local reinitialization refuses a non-local MySQL target")
		}
		plan.confirmation = fmt.Sprintf("mysql:%s@%s", parsed.DBName, host)
		plan.databaseTarget = plan.confirmation
		plan.databaseBackup = mysqlBackupDatabase(parsed.DBName, stamp)
	default:
		return reinitializePlan{}, fmt.Errorf(
			"local reinitialization does not support database driver %q",
			databaseConfig.Driver,
		)
	}
	return plan, nil
}

func executeReinitializePlan(ctx context.Context, plan reinitializePlan, now time.Time) error {
	fresh, err := buildReinitializePlan(plan.configPath, now)
	if err != nil {
		return fmt.Errorf("revalidate reinitialization target: %w", err)
	}
	if fresh.confirmation != plan.confirmation || fresh.database != plan.database ||
		fresh.backupDirectory != plan.backupDirectory {
		return errors.New("reinitialization target changed after confirmation")
	}
	currentInfo, err := os.Lstat(plan.configPath)
	if err != nil || !os.SameFile(plan.configIdentity, currentInfo) {
		return errors.New("installation configuration changed after confirmation")
	}
	if err := ensurePrivateBackupRoot(filepath.Dir(plan.backupDirectory)); err != nil {
		return err
	}
	if err := os.Mkdir(plan.backupDirectory, 0o700); err != nil {
		return fmt.Errorf("create unique reinitialization backup: %w", err)
	}
	backupConfig := filepath.Join(plan.backupDirectory, "aginex-config.json")
	if err := os.Rename(plan.configPath, backupConfig); err != nil {
		return fmt.Errorf("archive installation configuration: %w", err)
	}
	restoreConfiguration := true
	defer func() {
		if restoreConfiguration {
			_ = os.Rename(backupConfig, plan.configPath)
		}
	}()

	manifest := reinitializeManifest{
		CreatedAt:         now.UTC(),
		ConfigurationPath: plan.configPath,
		DatabaseDriver:    plan.database.Driver,
		DatabaseTarget:    plan.databaseTarget,
		DatabaseBackup:    plan.databaseBackup,
	}
	if err := writeReinitializeManifest(plan.backupDirectory, manifest); err != nil {
		return err
	}
	if _, err := archiveReinitializeDatabase(ctx, plan); err != nil {
		return fmt.Errorf("archive local database; configuration was restored: %w", err)
	}
	restoreConfiguration = false
	return nil
}

func archiveReinitializeDatabase(ctx context.Context, plan reinitializePlan) (string, error) {
	switch plan.database.Driver {
	case "sqlite":
		return archiveSQLiteDatabase(ctx, plan.database.DSN, plan.databaseBackup)
	case "postgres":
		return archivePostgresDatabase(ctx, plan.database, plan.databaseBackup)
	case "mysql":
		return archiveMySQLDatabase(ctx, plan.database, plan.databaseBackup)
	default:
		return "", fmt.Errorf("unsupported database driver %q", plan.database.Driver)
	}
}

func archiveSQLiteDatabase(ctx context.Context, source, destination string) (string, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return "", fmt.Errorf("inspect SQLite database: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("SQLite database must be a regular non-symbolic file")
	}
	sqlDB, err := openReinitializeDatabase(ctx, config.Database{
		Driver: "sqlite",
		DSN:    source,
	})
	if err != nil {
		return "", err
	}
	var busy, logFrames, checkpointedFrames int
	checkpointErr := sqlDB.QueryRowContext(ctx,
		`PRAGMA wal_checkpoint(TRUNCATE)`,
	).Scan(&busy, &logFrames, &checkpointedFrames)
	closeErr := sqlDB.Close()
	if checkpointErr != nil {
		return "", fmt.Errorf("checkpoint SQLite database before backup: %w", checkpointErr)
	}
	if busy != 0 {
		return "", errors.New("SQLite database is busy; stop the API and worker before reinitializing")
	}
	if closeErr != nil {
		return "", fmt.Errorf("close SQLite database before backup: %w", closeErr)
	}

	type movedFile struct{ source, destination string }
	var moved []movedFile
	rollback := func() {
		for index := len(moved) - 1; index >= 0; index-- {
			_ = moveFileRecoverably(moved[index].destination, moved[index].source, 0o600)
		}
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := source + suffix
		if _, err := os.Lstat(sidecar); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", fmt.Errorf("inspect SQLite sidecar: %w", err)
		}
		sidecarDestination := destination + suffix
		if err := moveFileRecoverably(sidecar, sidecarDestination, 0o600); err != nil {
			rollback()
			return "", fmt.Errorf("archive SQLite sidecar: %w", err)
		}
		moved = append(moved, movedFile{source: sidecar, destination: sidecarDestination})
	}
	if err := moveFileRecoverably(source, destination, 0o600); err != nil {
		rollback()
		return "", fmt.Errorf("archive SQLite database: %w", err)
	}
	return destination, nil
}

func archivePostgresDatabase(
	ctx context.Context,
	databaseConfig config.Database,
	backupSchema string,
) (string, error) {
	sqlDB, err := openReinitializeDatabase(ctx, databaseConfig)
	if err != nil {
		return "", err
	}
	defer sqlDB.Close()
	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin PostgreSQL archive: %w", err)
	}
	defer tx.Rollback()
	if err := archivePostgresObjects(ctx, tx, backupSchema); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit PostgreSQL archive: %w", err)
	}
	return backupSchema, nil
}

func archivePostgresObjects(
	ctx context.Context,
	tx *sql.Tx,
	backupSchema string,
) error {
	var owner string
	var canCreateSchema bool
	if err := tx.QueryRowContext(ctx,
		`SELECT current_user,
		        has_database_privilege(current_user, current_database(), 'CREATE')`,
	).Scan(&owner, &canCreateSchema); err != nil {
		return fmt.Errorf("inspect PostgreSQL archive authority: %w", err)
	}
	if !canCreateSchema {
		return errors.New(
			"PostgreSQL application role needs CREATE privilege on the local database to make a recoverable backup schema",
		)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`,
		backupSchema,
	).Scan(&exists); err != nil {
		return fmt.Errorf("inspect PostgreSQL backup schema: %w", err)
	}
	if exists {
		return fmt.Errorf("PostgreSQL backup schema %q already exists", backupSchema)
	}
	if err := rejectUnsupportedPostgresTypes(ctx, tx); err != nil {
		return err
	}
	relations, err := postgresPublicRelations(ctx, tx)
	if err != nil {
		return err
	}
	if err := validatePostgresRelationOwnership(relations, owner); err != nil {
		return err
	}
	routines, err := postgresPublicRoutines(ctx, tx)
	if err != nil {
		return err
	}
	if err := validatePostgresRoutineOwnership(routines, owner); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`CREATE SCHEMA `+quotePostgresIdentifier(backupSchema)+
			` AUTHORIZATION `+quotePostgresIdentifier(owner),
	); err != nil {
		return fmt.Errorf("create PostgreSQL backup schema: %w", err)
	}

	sort.Slice(relations, func(left, right int) bool {
		leftRank := postgresRelationMoveRank(relations[left].kind)
		rightRank := postgresRelationMoveRank(relations[right].kind)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		return relations[left].name < relations[right].name
	})
	for _, relation := range relations {
		if relation.kind == "S" {
			continue
		}
		if err := movePostgresRelation(ctx, tx, relation, backupSchema); err != nil {
			return err
		}
	}

	// ALTER TABLE SET SCHEMA also moves owned sequences. Re-read the catalog so
	// only sequences that genuinely remain in public are moved explicitly.
	remaining, err := postgresPublicRelations(ctx, tx)
	if err != nil {
		return err
	}
	if err := validatePostgresRelationOwnership(remaining, owner); err != nil {
		return err
	}
	for _, relation := range remaining {
		if relation.kind != "S" {
			return fmt.Errorf(
				"PostgreSQL public schema changed during archive; relation %q was not in the preflight inventory",
				relation.name,
			)
		}
		if err := movePostgresRelation(ctx, tx, relation, backupSchema); err != nil {
			return err
		}
	}
	for _, routine := range routines {
		if err := movePostgresRoutine(ctx, tx, routine, backupSchema); err != nil {
			return err
		}
	}
	postflight, err := postgresPublicRelations(ctx, tx)
	if err != nil {
		return err
	}
	if len(postflight) != 0 {
		return fmt.Errorf(
			"PostgreSQL public schema still contains relation %q after archive",
			postflight[0].name,
		)
	}
	remainingRoutines, err := postgresPublicRoutines(ctx, tx)
	if err != nil {
		return err
	}
	if len(remainingRoutines) != 0 {
		return fmt.Errorf(
			"PostgreSQL public schema still contains routine %q after archive",
			remainingRoutines[0].name,
		)
	}
	return nil
}

func rejectUnsupportedPostgresTypes(ctx context.Context, tx *sql.Tx) error {
	var name string
	err := tx.QueryRowContext(ctx,
		`SELECT t.typname
		 FROM pg_type t
		 JOIN pg_namespace n ON n.oid = t.typnamespace
		 WHERE n.nspname = 'public'
		   AND t.typrelid = 0
		   AND t.typelem = 0
		   AND NOT EXISTS (
		     SELECT 1 FROM pg_depend d
		     WHERE d.classid = 'pg_type'::regclass
		       AND d.objid = t.oid
		       AND d.deptype = 'e'
		   )
		 ORDER BY t.typname
		 LIMIT 1`,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect PostgreSQL standalone objects: %w", err)
	}
	return fmt.Errorf(
		"PostgreSQL public schema contains unsupported standalone type %q; archive it manually before reinitializing",
		name,
	)
}

func postgresPublicRoutines(ctx context.Context, tx *sql.Tx) ([]postgresRoutine, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT p.proname,
		        pg_get_function_identity_arguments(p.oid),
		        p.prokind::text,
		        pg_get_userbyid(p.proowner)
		 FROM pg_proc p
		 JOIN pg_namespace n ON n.oid = p.pronamespace
		 WHERE n.nspname = 'public'
		   AND NOT EXISTS (
		     SELECT 1 FROM pg_depend d
		     WHERE d.classid = 'pg_proc'::regclass
		       AND d.objid = p.oid
		       AND d.deptype = 'e'
		   )
		 ORDER BY p.proname, pg_get_function_identity_arguments(p.oid)`,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL public routines: %w", err)
	}
	defer rows.Close()
	var routines []postgresRoutine
	for rows.Next() {
		var routine postgresRoutine
		if err := rows.Scan(
			&routine.name,
			&routine.identityArguments,
			&routine.kind,
			&routine.owner,
		); err != nil {
			return nil, fmt.Errorf("inspect PostgreSQL public routine: %w", err)
		}
		routines = append(routines, routine)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL public routines: %w", err)
	}
	return routines, nil
}

func validatePostgresRoutineOwnership(routines []postgresRoutine, owner string) error {
	for _, routine := range routines {
		if routine.owner != owner {
			return fmt.Errorf(
				"PostgreSQL public routine %q is owned by another role; archive it manually before reinitializing",
				routine.name,
			)
		}
		if routine.kind != "f" {
			return fmt.Errorf(
				"PostgreSQL public schema contains unsupported routine %q of kind %q; archive it manually before reinitializing",
				routine.name,
				routine.kind,
			)
		}
	}
	return nil
}

func movePostgresRoutine(
	ctx context.Context,
	tx *sql.Tx,
	routine postgresRoutine,
	backupSchema string,
) error {
	statement := `ALTER FUNCTION public.` + quotePostgresIdentifier(routine.name) +
		`(` + routine.identityArguments + `) SET SCHEMA ` +
		quotePostgresIdentifier(backupSchema)
	if _, err := tx.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("archive PostgreSQL routine %q: %w", routine.name, err)
	}
	return nil
}

func postgresPublicRelations(ctx context.Context, tx *sql.Tx) ([]postgresRelation, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT c.relname, c.relkind::text, pg_get_userbyid(c.relowner)
		 FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = 'public'
		   AND c.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
		 ORDER BY c.relname`,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL public relations: %w", err)
	}
	defer rows.Close()
	var relations []postgresRelation
	for rows.Next() {
		var relation postgresRelation
		if err := rows.Scan(&relation.name, &relation.kind, &relation.owner); err != nil {
			return nil, fmt.Errorf("inspect PostgreSQL public relation: %w", err)
		}
		relations = append(relations, relation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect PostgreSQL public relations: %w", err)
	}
	return relations, nil
}

func validatePostgresRelationOwnership(relations []postgresRelation, owner string) error {
	for _, relation := range relations {
		if relation.owner != owner {
			return fmt.Errorf(
				"PostgreSQL public relation %q is owned by another role; archive it manually before reinitializing",
				relation.name,
			)
		}
	}
	return nil
}

func postgresRelationMoveRank(kind string) int {
	switch kind {
	case "r", "p", "f":
		return 0
	case "v", "m":
		return 1
	case "S":
		return 2
	default:
		return 3
	}
}

func movePostgresRelation(
	ctx context.Context,
	tx *sql.Tx,
	relation postgresRelation,
	backupSchema string,
) error {
	statement, err := postgresRelationMoveStatement(relation, backupSchema)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("archive PostgreSQL relation %q: %w", relation.name, err)
	}
	return nil
}

func postgresRelationMoveStatement(
	relation postgresRelation,
	backupSchema string,
) (string, error) {
	var objectType string
	switch relation.kind {
	case "r", "p":
		objectType = "TABLE"
	case "v":
		objectType = "VIEW"
	case "m":
		objectType = "MATERIALIZED VIEW"
	case "f":
		objectType = "FOREIGN TABLE"
	case "S":
		objectType = "SEQUENCE"
	default:
		return "", fmt.Errorf(
			"PostgreSQL relation %q has unsupported kind %q",
			relation.name,
			relation.kind,
		)
	}
	return `ALTER ` + objectType + ` public.` + quotePostgresIdentifier(relation.name) +
		` SET SCHEMA ` + quotePostgresIdentifier(backupSchema), nil
}

func archiveMySQLDatabase(
	ctx context.Context,
	databaseConfig config.Database,
	backupDatabase string,
) (string, error) {
	parsed, err := mysqldriver.ParseDSN(databaseConfig.DSN)
	if err != nil {
		return "", errors.New("parse MySQL database target")
	}
	sqlDB, err := openReinitializeDatabase(ctx, databaseConfig)
	if err != nil {
		return "", err
	}
	defer sqlDB.Close()
	var charset, collation string
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT default_character_set_name, default_collation_name
		 FROM information_schema.schemata WHERE schema_name = ?`,
		parsed.DBName,
	).Scan(&charset, &collation); err != nil {
		return "", fmt.Errorf("inspect MySQL database defaults: %w", err)
	}
	if !safeSQLNamePattern.MatchString(charset) || !safeSQLNamePattern.MatchString(collation) {
		return "", errors.New("MySQL database uses unsupported character metadata")
	}
	var exists int
	if err := sqlDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = ?`,
		backupDatabase,
	).Scan(&exists); err != nil {
		return "", fmt.Errorf("inspect MySQL backup database: %w", err)
	}
	if exists != 0 {
		return "", fmt.Errorf("MySQL backup database %q already exists", backupDatabase)
	}
	tables, err := mysqlBaseTables(ctx, sqlDB, parsed.DBName)
	if err != nil {
		return "", err
	}
	if _, err := sqlDB.ExecContext(ctx,
		`CREATE DATABASE `+quoteMySQLIdentifier(backupDatabase)+
			` CHARACTER SET `+charset+` COLLATE `+collation,
	); err != nil {
		return "", fmt.Errorf("create MySQL backup database: %w", err)
	}
	if len(tables) > 0 {
		renames := make([]string, 0, len(tables))
		for _, table := range tables {
			renames = append(renames,
				quoteMySQLIdentifier(parsed.DBName)+"."+quoteMySQLIdentifier(table)+
					" TO "+quoteMySQLIdentifier(backupDatabase)+"."+quoteMySQLIdentifier(table),
			)
		}
		if _, err := sqlDB.ExecContext(ctx, `RENAME TABLE `+strings.Join(renames, ", ")); err != nil {
			return "", fmt.Errorf("archive MySQL tables: %w", err)
		}
	}
	return backupDatabase, nil
}

func mysqlBaseTables(ctx context.Context, db *sql.DB, databaseName string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT table_name, table_type FROM information_schema.tables
		 WHERE table_schema = ? ORDER BY table_name`,
		databaseName,
	)
	if err != nil {
		return nil, fmt.Errorf("inspect MySQL tables: %w", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name, tableType string
		if err := rows.Scan(&name, &tableType); err != nil {
			return nil, fmt.Errorf("inspect MySQL table: %w", err)
		}
		if tableType != "BASE TABLE" {
			return nil, fmt.Errorf("MySQL database contains unsupported %s %q", tableType, name)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect MySQL tables: %w", err)
	}
	sort.Strings(tables)
	return tables, nil
}

func openReinitializeDatabase(
	ctx context.Context,
	databaseConfig config.Database,
) (*sql.DB, error) {
	db, err := database.OpenContext(ctx, databaseConfig)
	if err != nil {
		return nil, fmt.Errorf("open local database for reinitialization: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access local database for reinitialization: %w", err)
	}
	return sqlDB, nil
}

func inspectReinitializeInstallation(
	path string,
) (reinitializeInstallation, os.FileInfo, error) {
	payload, identity, err := readSecureReinitializeFile(path)
	if err != nil {
		return reinitializeInstallation{}, nil, err
	}
	var document map[string]json.RawMessage
	if err := decodeStrictJSONDocument(payload, &document); err != nil {
		return reinitializeInstallation{}, nil, err
	}
	allowed := map[string]struct{}{
		"version": {}, "revision": {}, "database": {}, "sessionSecret": {},
		"installedAt": {}, "updatedAt": {}, "activeProfileId": {},
		"profiles": {}, "fileUploadPolicy": {},
	}
	for field := range document {
		if _, ok := allowed[field]; !ok {
			return reinitializeInstallation{}, nil, fmt.Errorf(
				"installation configuration contains unsupported field %q",
				field,
			)
		}
	}
	var result reinitializeInstallation
	if err := json.Unmarshal(document["version"], &result.Version); err != nil ||
		result.Version < 1 || result.Version > 3 {
		return reinitializeInstallation{}, nil, errors.New(
			"reinitialization can inspect only known unpublished installation drafts v1-v3",
		)
	}
	if err := decodeStrictJSONDocument(document["database"], &result.Database); err != nil {
		return reinitializeInstallation{}, nil, fmt.Errorf("decode reinitialization database target: %w", err)
	}
	return result, identity, nil
}

func readSecureReinitializeFile(path string) ([]byte, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect installation configuration: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nil, errors.New("installation configuration must be a regular non-symbolic file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, nil, errors.New("installation configuration permissions must be 0600")
	}
	if info.Size() > 64<<10 {
		return nil, nil, errors.New("installation configuration exceeds 64 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open installation configuration: %w", err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, nil, errors.New("installation configuration changed while opening")
	}
	current, err := os.Lstat(path)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(current, opened) {
		return nil, nil, errors.New("installation configuration changed while opening")
	}
	payload, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil {
		return nil, nil, fmt.Errorf("read installation configuration: %w", err)
	}
	if len(payload) > 64<<10 {
		return nil, nil, errors.New("installation configuration exceeds 64 KiB")
	}
	return payload, opened, nil
}

func decodeStrictJSONDocument(payload []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func localSQLitePath(dsn string) (string, error) {
	value := strings.TrimSpace(dsn)
	if value == "" || value == ":memory:" || strings.HasPrefix(value, "file:") ||
		strings.Contains(value, "?") {
		return "", errors.New("local reinitialization requires a file-backed SQLite path without URI options")
	}
	absolute, err := filepath.Abs(filepath.Clean(value))
	if err != nil {
		return "", fmt.Errorf("resolve SQLite database path: %w", err)
	}
	return absolute, nil
}

func localDatabaseHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" || strings.HasPrefix(host, "/") {
		return true
	}
	address := net.ParseIP(strings.Trim(host, "[]"))
	return address != nil && address.IsLoopback()
}

func localMySQLAddress(network, address string) (string, bool) {
	if network == "unix" {
		return "local-socket", filepath.IsAbs(address)
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return "", false
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil || !localDatabaseHost(host) {
		return "", false
	}
	return net.JoinHostPort(host, port), true
}

func postgresBackupSchema(stamp string) string {
	return "aginex_backup_" + strings.ToLower(strings.ReplaceAll(stamp, "Z", "z"))
}

func mysqlBackupDatabase(databaseName, stamp string) string {
	digest := sha256.Sum256([]byte(databaseName))
	return fmt.Sprintf(
		"aginex_backup_%s_%x",
		strings.ToLower(strings.ReplaceAll(stamp, "Z", "z")),
		digest[:4],
	)
}

func reservedPostgresDatabase(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "postgres", "template0", "template1":
		return true
	default:
		return false
	}
}

func reservedMySQLDatabase(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "information_schema", "mysql", "performance_schema", "sys":
		return true
	default:
		return false
	}
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func quoteMySQLIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func moveFileRecoverably(source, destination string, mode os.FileMode) error {
	if err := os.Chmod(source, mode); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		_ = output.Close()
		if !completed {
			_ = os.Remove(destination)
		}
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Remove(source); err != nil {
		return err
	}
	completed = true
	return nil
}

func ensurePrivateBackupRoot(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create reinitialization backup parent: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect reinitialization backup parent: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("reinitialization backup parent must be a private real directory")
	}
	return nil
}

func writeReinitializeManifest(directory string, manifest reinitializeManifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode reinitialization manifest: %w", err)
	}
	payload = append(payload, '\n')
	path := filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write reinitialization manifest: %w", err)
	}
	return nil
}
