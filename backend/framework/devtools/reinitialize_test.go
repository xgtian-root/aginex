package devtools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/platform/database"
)

func TestReinitializeCommandIsDryRunUntilExactTargetIsConfirmed(t *testing.T) {
	fixture := newSQLiteReinitializeFixture(t)
	command := newReinitializeCommand(reinitializeDependencies{
		now: func() time.Time { return time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC) },
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"--config", fixture.configPath})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture.configPath); err != nil {
		t.Fatalf("dry run changed config: %v", err)
	}
	if _, err := os.Stat(fixture.databasePath); err != nil {
		t.Fatalf("dry run changed database: %v", err)
	}
	if !strings.Contains(output.String(), "No changes were made") ||
		!strings.Contains(output.String(), fixture.confirmation) {
		t.Fatalf("dry-run output = %q", output.String())
	}

	command = newReinitializeCommand(reinitializeDependencies{
		now: func() time.Time { return time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC) },
	})
	command.SetArgs([]string{
		"--config", fixture.configPath,
		"--confirm", "wrong-target",
	})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("wrong confirmation error = %v", err)
	}
	if _, err := os.Stat(fixture.configPath); err != nil {
		t.Fatalf("wrong confirmation changed config: %v", err)
	}
}

func TestReinitializeDryRunCanRetireKnownStaleDraftWithoutRuntimeCompatibility(
	t *testing.T,
) {
	fixture := newSQLiteReinitializeFixture(t)
	payload, err := os.ReadFile(fixture.configPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	document["version"] = json.RawMessage(`2`)
	delete(document, "fileUploadPolicy")
	payload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.configPath, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ReadInstallation(fixture.configPath); err == nil ||
		!strings.Contains(err.Error(), "unsupported installation configuration version") {
		t.Fatalf("runtime reader error = %v", err)
	}

	command := newReinitializeCommand(reinitializeDependencies{})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--config", fixture.configPath})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), fixture.confirmation) {
		t.Fatalf("stale-draft dry run output = %q", output.String())
	}
}

func TestReinitializeSQLiteArchivesConfigurationAndDatabase(t *testing.T) {
	fixture := newSQLiteReinitializeFixture(t)
	command := newReinitializeCommand(reinitializeDependencies{
		now: func() time.Time { return time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC) },
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{
		"--config", fixture.configPath,
		"--confirm", fixture.confirmation,
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fixture.configPath); !os.IsNotExist(err) {
		t.Fatalf("config still exists: %v", err)
	}
	if _, err := os.Stat(fixture.databasePath); !os.IsNotExist(err) {
		t.Fatalf("database still exists: %v", err)
	}
	backupDirectory := filepath.Join(
		filepath.Dir(fixture.configPath),
		"reinitialize-backups",
		"20260811T093000Z",
	)
	for _, path := range []string{
		filepath.Join(backupDirectory, "aginex-config.json"),
		filepath.Join(backupDirectory, "database.sqlite"),
		filepath.Join(backupDirectory, "manifest.json"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("backup %s: %v", path, err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("backup permissions %s = %o", path, info.Mode().Perm())
		}
	}
	archivedInstallation, err := config.ReadInstallation(
		filepath.Join(backupDirectory, "aginex-config.json"),
	)
	if err != nil || archivedInstallation.Version != config.CurrentInstallationVersion {
		t.Fatalf("archived installation = %#v, error = %v", archivedInstallation, err)
	}
	archivedDatabase, err := database.OpenContext(t.Context(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(backupDirectory, "database.sqlite"),
	})
	if err != nil {
		t.Fatal(err)
	}
	archivedSQL, err := archivedDatabase.DB()
	if err != nil {
		t.Fatal(err)
	}
	var retainedTable string
	if err := archivedSQL.QueryRowContext(t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'retained_before_reset'`,
	).Scan(&retainedTable); err != nil {
		t.Fatalf("recover archived SQLite database: %v", err)
	}
	if err := archivedSQL.Close(); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(backupDirectory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(manifest, []byte("0123456789abcdef")) ||
		bytes.Contains(manifest, []byte("sessionSecret")) {
		t.Fatal("manifest contains installation secrets")
	}
	t.Setenv("AGINEX_CONFIG_FILE", fixture.configPath)
	t.Setenv("AGINEX_ENV", "development")
	t.Setenv("AGINEX_DATABASE_DRIVER", "")
	t.Setenv("AGINEX_DATABASE_DSN", "")
	t.Setenv("AGINEX_SESSION_SECRET", "")
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_EMAIL", "")
	t.Setenv("AGINEX_BOOTSTRAP_ADMIN_PASSWORD", "")
	t.Setenv("AGINEX_JOBS_DRIVER", "disabled")
	t.Setenv("AGINEX_SESSION_SECURE", "false")
	t.Setenv("AGINEX_API_PUBLIC_URL", "http://localhost:8080")
	t.Setenv("AGINEX_WEB_ORIGINS", "http://localhost:3000")
	state, err := config.LoadState()
	if err != nil || state.Status != config.StatusSetup {
		t.Fatalf("post-reinitialize state = %#v, error = %v", state, err)
	}
	if !strings.Contains(output.String(), "Reinitialization complete") {
		t.Fatalf("execution output = %q", output.String())
	}
}

func TestPostgresReinitializeArchivesObjectsWithoutOwningPublicSchema(t *testing.T) {
	relations := []postgresRelation{
		{name: `users`, kind: "r", owner: "aginex"},
		{name: `report view`, kind: "v", owner: "aginex"},
		{name: `events_id_seq`, kind: "S", owner: "aginex"},
	}
	if err := validatePostgresRelationOwnership(relations, "aginex"); err != nil {
		t.Fatal(err)
	}
	for _, relation := range relations {
		statement, err := postgresRelationMoveStatement(
			relation,
			`aginex_backup_20260811t093000z`,
		)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(statement, "ALTER SCHEMA") ||
			strings.Contains(statement, "CREATE SCHEMA public") {
			t.Fatalf("archive statement changes public schema ownership: %q", statement)
		}
		if !strings.Contains(statement, `public.`) ||
			!strings.Contains(statement, `SET SCHEMA "aginex_backup_20260811t093000z"`) {
			t.Fatalf("archive statement = %q", statement)
		}
	}
	if err := validatePostgresRelationOwnership(
		[]postgresRelation{{name: "foreign_owned", kind: "r", owner: "postgres"}},
		"aginex",
	); err == nil || !strings.Contains(err.Error(), "another role") {
		t.Fatalf("foreign owner error = %v", err)
	}
	if _, err := postgresRelationMoveStatement(
		postgresRelation{name: "unsupported", kind: "x", owner: "aginex"},
		"backup",
	); err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("unsupported relation error = %v", err)
	}
	if err := validatePostgresRoutineOwnership([]postgresRoutine{{
		name: "aginex_prevent_audit_mutation", kind: "f", owner: "aginex",
	}}, "aginex"); err != nil {
		t.Fatalf("application function rejected: %v", err)
	}
	if err := validatePostgresRoutineOwnership([]postgresRoutine{{
		name: "unsafe_procedure", kind: "p", owner: "aginex",
	}}, "aginex"); err == nil || !strings.Contains(err.Error(), "unsupported routine") {
		t.Fatalf("unsupported routine error = %v", err)
	}
}

func TestReinitializeRejectsProductionEnvironmentAndRemoteDatabase(t *testing.T) {
	t.Run("production", func(t *testing.T) {
		fixture := newSQLiteReinitializeFixture(t)
		command := newReinitializeCommand(reinitializeDependencies{
			environment: func() string { return "production" },
		})
		command.SetArgs([]string{"--config", fixture.configPath})
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "production") {
			t.Fatalf("production error = %v", err)
		}
	})

	t.Run("remote postgres", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "aginex-config.json")
		installation, err := config.NewManagedInstallation(
			config.Database{
				Driver: "postgres",
				DSN:    "postgres://user:secret@example.com/aginex?sslmode=require",
			},
			"0123456789abcdef0123456789abcdef",
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := config.CommitInstallation(path, installation); err != nil {
			t.Fatal(err)
		}
		command := newReinitializeCommand(reinitializeDependencies{})
		command.SetArgs([]string{"--config", path})
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "local") {
			t.Fatalf("remote database error = %v", err)
		}
	})
}

func TestReinitializeRejectsSymlinkedBackupRootWithoutChangingState(t *testing.T) {
	fixture := newSQLiteReinitializeFixture(t)
	backupRoot := filepath.Join(filepath.Dir(fixture.configPath), "reinitialize-backups")
	if err := os.Symlink(t.TempDir(), backupRoot); err != nil {
		t.Fatal(err)
	}
	command := newReinitializeCommand(reinitializeDependencies{
		now: func() time.Time { return time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC) },
	})
	command.SetArgs([]string{
		"--config", fixture.configPath,
		"--confirm", fixture.confirmation,
	})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "private real directory") {
		t.Fatalf("symlinked backup root error = %v", err)
	}
	for _, path := range []string{fixture.configPath, fixture.databasePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("rejected reset changed %s: %v", path, err)
		}
	}
}

type sqliteReinitializeFixture struct {
	configPath   string
	databasePath string
	confirmation string
}

func newSQLiteReinitializeFixture(t *testing.T) sqliteReinitializeFixture {
	t.Helper()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "aginex.db")
	db, err := database.OpenContext(t.Context(), config.Database{
		Driver: "sqlite",
		DSN:    databasePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(context.Background(),
		`CREATE TABLE retained_before_reset (id INTEGER PRIMARY KEY)`,
	); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(directory, "aginex-config.json")
	installation, err := config.NewManagedInstallation(
		config.Database{Driver: "sqlite", DSN: databasePath},
		"0123456789abcdef0123456789abcdef",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.CommitInstallation(configPath, installation); err != nil {
		t.Fatal(err)
	}
	return sqliteReinitializeFixture{
		configPath:   configPath,
		databasePath: databasePath,
		confirmation: "sqlite:" + filepath.Clean(databasePath),
	}
}
