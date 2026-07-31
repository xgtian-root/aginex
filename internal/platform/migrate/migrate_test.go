package migrate

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"github.com/pressly/goose/v3"
)

func TestLatestVersionMatchesEveryDialect(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			version, err := Latest(driver)
			if err != nil {
				t.Fatal(err)
			}
			if version != 8 {
				t.Fatalf("latest version = %d, want 8", version)
			}
		})
	}
}

func TestSQLiteStatusOnEmptyDatabase(t *testing.T) {
	db := openSQLite(t)

	status, err := Status(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 0 || status.Latest != 8 {
		t.Fatalf("versions = current %d latest %d, want 0 and 8", status.Current, status.Latest)
	}
	if status.Applied != 0 || status.Pending != 8 || status.IsCurrent() {
		t.Fatalf("status = %#v, want empty database with eight pending migrations", status)
	}
	if status.RateLimit.Current != 0 || status.RateLimit.Latest != 1 || status.RateLimit.Pending != 1 {
		t.Fatalf("rate limit status = %#v, want one pending migration", status.RateLimit)
	}
	if err := EnsureCurrent(context.Background(), db, "sqlite"); !errors.Is(err, ErrSchemaNotCurrent) {
		t.Fatalf("EnsureCurrent error = %v, want ErrSchemaNotCurrent", err)
	}
	var versionTableCount int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'",
	).Scan(&versionTableCount); err != nil {
		t.Fatal(err)
	}
	if versionTableCount != 0 {
		t.Fatal("status check created goose_db_version on an empty database")
	}
}

func TestSQLiteStatusAtLatestVersion(t *testing.T) {
	db := openSQLite(t)
	if err := Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}

	status, err := Status(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 8 || status.Latest != 8 {
		t.Fatalf("versions = current %d latest %d, want 8 and 8", status.Current, status.Latest)
	}
	if status.Applied != 8 || status.Pending != 0 || !status.IsCurrent() {
		t.Fatalf("status = %#v, want current database", status)
	}
	if status.RateLimit.Current != 1 || status.RateLimit.Latest != 1 || status.RateLimit.Pending != 0 {
		t.Fatalf("rate limit status = %#v, want current", status.RateLimit)
	}
	if err := EnsureCurrent(context.Background(), db, "sqlite"); err != nil {
		t.Fatalf("EnsureCurrent error = %v", err)
	}
}

func TestEnsureCurrentRejectsMissingRequiredRateLimitModule(t *testing.T) {
	db := openSQLite(t)
	provider, err := newProvider(db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	status, err := Status(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 8 || status.RateLimit.Pending != 1 || status.IsCurrent() {
		t.Fatalf("status = %#v, want current core and pending rate limit module", status)
	}
	if err := EnsureCurrent(context.Background(), db, "sqlite"); !errors.Is(err, ErrSchemaNotCurrent) {
		t.Fatalf("EnsureCurrent error = %v, want ErrSchemaNotCurrent", err)
	}
}

func TestSQLiteStatusBehindLatestVersion(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 1)

	status, err := Status(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 1 || status.Latest != 8 {
		t.Fatalf("versions = current %d latest %d, want 1 and 8", status.Current, status.Latest)
	}
	if status.Applied != 1 || status.Pending != 7 || status.IsCurrent() {
		t.Fatalf("status = %#v, want seven pending migrations", status)
	}
	if err := EnsureCurrent(context.Background(), db, "sqlite"); !errors.Is(err, ErrSchemaNotCurrent) {
		t.Fatalf("EnsureCurrent error = %v, want ErrSchemaNotCurrent", err)
	}

	if err := Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	current, err := Current(context.Background(), db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if current != 8 {
		t.Fatalf("current version = %d, want 8", current)
	}
}

func TestSQLiteAuditLogsAreAppendOnly(t *testing.T) {
	db := openSQLite(t)
	if err := Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO audit_logs (
			id, actor_id, actor_kind, action, resource, resource_id, result,
			source, summary, request_id, ip_address, sanitized_before,
			sanitized_after, created_at
		) VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"audit-append-only",
		"system",
		"system:test",
		"test",
		"resource-1",
		"success",
		"system",
		"Append-only test",
		"request-1",
		"127.0.0.1",
		"{}",
		"{}",
		"2026-07-31T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(
		"UPDATE audit_logs SET summary = ? WHERE id = ?",
		"tampered",
		"audit-append-only",
	); err == nil {
		t.Fatal("audit update succeeded, want append-only rejection")
	}
	if _, err := db.Exec(
		"DELETE FROM audit_logs WHERE id = ?",
		"audit-append-only",
	); err == nil {
		t.Fatal("audit deletion succeeded, want append-only rejection")
	}

	var summary string
	if err := db.QueryRow(
		"SELECT summary FROM audit_logs WHERE id = ?",
		"audit-append-only",
	).Scan(&summary); err != nil {
		t.Fatal(err)
	}
	if summary != "Append-only test" {
		t.Fatalf("stored summary = %q", summary)
	}
}

func TestSQLiteCoreMigrationsExcludeBundledBusinessTables(t *testing.T) {
	db := openSQLite(t)
	if err := Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"products", "file_objects"} {
		var count int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("core migrations created bundled table %q", table)
		}
	}
}

func TestSQLiteAuditContextMigrationFromEmptyDatabase(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 3)

	columns := sqliteColumns(t, db, "audit_logs")
	for _, name := range []string{
		"actor_kind",
		"result",
		"source",
		"sanitized_before",
		"sanitized_after",
	} {
		if !columns[name] {
			t.Errorf("audit_logs is missing %s", name)
		}
	}
}

func TestSQLiteAuditContextMigrationUpgradesPreviousVersion(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 2)
	if _, err := db.Exec(`
		INSERT INTO audit_logs (
			id, actor_id, action, resource, resource_id, summary, request_id, ip_address, created_at
		) VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?)
	`,
		"audit-legacy",
		"products:create",
		"product",
		"product-1",
		"Created product",
		"request-1",
		"127.0.0.1",
		"2026-07-30T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}

	upSQLiteTo(t, db, 3)

	var actorKind, result, source, before, after string
	if err := db.QueryRow(`
		SELECT actor_kind, result, source, sanitized_before, sanitized_after
		FROM audit_logs
		WHERE id = ?
	`, "audit-legacy").Scan(&actorKind, &result, &source, &before, &after); err != nil {
		t.Fatal(err)
	}
	if actorKind != "system" || result != "success" || source != "http" {
		t.Fatalf("backfill = actor %q result %q source %q", actorKind, result, source)
	}
	if before != "{}" || after != "{}" {
		t.Fatalf("sanitized JSON defaults = before %q after %q", before, after)
	}
}

func TestSQLiteAuditContextMigrationRollsBackToPreviousVersion(t *testing.T) {
	db := openSQLite(t)
	upSQLiteTo(t, db, 3)
	if _, err := db.Exec(`
		INSERT INTO audit_logs (
			id, actor_id, actor_kind, action, resource, resource_id, result, source,
			summary, request_id, ip_address, sanitized_before, sanitized_after, created_at
		) VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		"audit-1",
		"system",
		"products:create",
		"product",
		"product-1",
		"success",
		"worker",
		"Created product",
		"request-1",
		"127.0.0.1",
		`{"status":"draft"}`,
		`{"status":"active"}`,
		"2026-07-30T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}

	provider, err := newProvider(db, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(context.Background(), 2); err != nil {
		t.Fatal(err)
	}

	columns := sqliteColumns(t, db, "audit_logs")
	for _, name := range []string{
		"actor_kind",
		"result",
		"source",
		"sanitized_before",
		"sanitized_after",
	} {
		if columns[name] {
			t.Errorf("audit_logs retained rolled-back column %s", name)
		}
	}
	var action string
	if err := db.QueryRow("SELECT action FROM audit_logs WHERE id = ?", "audit-1").Scan(&action); err != nil {
		t.Fatal(err)
	}
	if action != "products:create" {
		t.Fatalf("legacy audit action = %q", action)
	}
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "aginex.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func upSQLiteTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrationFS)
	if err := goose.UpTo(db, "migrations/sqlite", version); err != nil {
		t.Fatal(err)
	}
}

func sqliteColumns(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}
