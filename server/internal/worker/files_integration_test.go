package worker

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/server/framework/services"
	internalapp "github.com/xgtian-root/aginex/server/internal/app"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"github.com/xgtian-root/aginex/server/internal/platform/multipartcleanup"
)

// This dedicated DSN avoids sharing a database with destructive migration
// matrix tests. Each execution also owns a fresh schema and only drops that
// schema during cleanup; worker construction itself must be read-only.
func TestFilesWorkerServicesAndReadOnlyMigrations(t *testing.T) {
	dsn := os.Getenv("AGINEX_FILES_WORKER_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGINEX_FILES_WORKER_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "aginex_files_worker_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop isolated worker test schema: %v", err)
		}
	})
	scopedDSN := dsn + " search_path=" + schema
	if parsed, err := url.Parse(dsn); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		scopedDSN = parsed.String()
	}
	cfg := config.WithDefaults(config.Config{
		Environment: "test",
		Database:    config.Database{Driver: "postgres", DSN: scopedDSN},
		Storage:     config.Storage{Driver: "local", LocalRoot: t.TempDir()},
		Jobs:        config.Jobs{Driver: "postgres", WorkerID: "files-worker-integration"},
	})
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := internalapp.MigrateUp(t.Context(), sqlDB, cfg); err != nil {
		t.Fatal(err)
	}
	coreTables := workerIntegrationTables(t, sqlDB)
	assertWorkerFileTables(t, coreTables, false)

	withoutFiles, err := NewWithModules(t.Context(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	if withoutFiles.services.Files != nil {
		t.Fatal("worker without FilesModule exposes a file service")
	}
	if len(withoutFiles.registry.JobHandlers()) != 0 {
		t.Fatalf("worker without FilesModule registered cleanup jobs: %#v", withoutFiles.registry.JobHandlers())
	}
	if got := workerIntegrationTables(t, sqlDB); !slices.Equal(got, coreTables) {
		t.Fatalf("worker without FilesModule changed schema: before=%v after=%v", coreTables, got)
	}

	if _, err := NewWithModules(t.Context(), cfg, db, internalapp.FilesModule()); err == nil || !strings.Contains(err.Error(), "module migrations") {
		t.Fatalf("worker accepted unmigrated FilesModule: %v", err)
	}
	if got := workerIntegrationTables(t, sqlDB); !slices.Equal(got, coreTables) {
		t.Fatalf("worker with unmigrated FilesModule changed schema: before=%v after=%v", coreTables, got)
	}

	if err := internalapp.MigrateUp(t.Context(), sqlDB, cfg, internalapp.FilesModule()); err != nil {
		t.Fatal(err)
	}
	fileTables := workerIntegrationTables(t, sqlDB)
	assertWorkerFileTables(t, fileTables, true)
	withFiles, err := NewWithModules(t.Context(), cfg, db, internalapp.FilesModule())
	if err != nil {
		t.Fatal(err)
	}
	if withFiles.services.Files == nil {
		t.Fatal("worker with migrated FilesModule lacks its public file service")
	}
	injected, ok := services.RuntimeFromContext(withFiles.contextWithRuntimeServices(t.Context()))
	if !ok || injected.Files != withFiles.services.Files {
		t.Fatal("worker job context did not receive the public file service")
	}
	handlers := withFiles.registry.JobHandlers()
	if len(handlers) != 4 {
		t.Fatalf("FilesModule worker registered %d handlers, want three cleanup versions and multipart cleanup", len(handlers))
	}
	for _, version := range []uint{fileCleanupJobVersionV1, fileCleanupJobVersionV2, fileCleanupJobVersionV3} {
		found := false
		for _, handler := range handlers {
			found = found || (handler.Type == fileCleanupJobType && handler.Version == version && handler.Handle != nil)
		}
		if !found {
			t.Errorf("worker lacks storage cleanup v%d", version)
		}
	}
	foundMultipart := false
	for _, handler := range handlers {
		foundMultipart = foundMultipart || (handler.Type == multipartcleanup.JobType && handler.Version == multipartcleanup.PayloadVersion && handler.Handle != nil)
	}
	if !foundMultipart {
		t.Error("worker lacks multipart cleanup handler")
	}
	if got := workerIntegrationTables(t, sqlDB); !slices.Equal(got, fileTables) {
		t.Fatalf("worker with migrated FilesModule changed schema: before=%v after=%v", fileTables, got)
	}
}

func workerIntegrationTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() ORDER BY table_name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func assertWorkerFileTables(t *testing.T, tables []string, want bool) {
	t.Helper()
	for _, table := range []string{"file_objects", "file_upload_sessions", "file_upload_parts", "file_reference_owners", "file_references", "aginex_files_migrations"} {
		if got := slices.Contains(tables, table); got != want {
			t.Errorf("table %s exists=%v, want %v", table, got, want)
		}
	}
}
