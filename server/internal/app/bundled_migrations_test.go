package app

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	coremigrate "github.com/xgtian-root/aginex/server/internal/platform/migrate"
)

func TestBundledFilesUsesSingleCurrentBaselinePerDialect(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		entries, err := bundledMigrationFiles.ReadDir(
			"migrations/files/" + dialect,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].IsDir() ||
			entries[0].Name() != "00001_files.sql" {
			t.Fatalf("%s Files migrations = %#v, want current baseline only", dialect, entries)
		}
		payload, err := bundledMigrationFiles.ReadFile(
			"migrations/files/" + dialect + "/00001_files.sql",
		)
		if err != nil {
			t.Fatal(err)
		}
		sql := string(payload)
		for _, fragment := range []string{
			"storage_profile_id",
			"CREATE TABLE IF NOT EXISTS file_upload_sessions",
			"CREATE TABLE IF NOT EXISTS file_upload_parts",
			"part_count BETWEEN 2 AND 32",
			"part_number BETWEEN 1 AND 32",
			"part_size = 33554432",
		} {
			if !strings.Contains(sql, fragment) {
				t.Errorf("%s current Files baseline is missing %q", dialect, fragment)
			}
		}
		if strings.Contains(sql, "10000") {
			t.Errorf("%s current Files baseline retained the obsolete part bound", dialect)
		}
	}
}

func TestBundledSQLiteMigrationsCreateCompleteExplicitModuleTables(
	t *testing.T,
) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(
		t.Context(),
		db,
		"sqlite",
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatal(err)
	}

	for table, columns := range map[string][]string{
		"file_objects": {
			"id",
			"storage_profile_id",
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
		"file_upload_sessions": {
			"id",
			"file_id",
			"provider_upload_id",
			"resume_fingerprint",
			"part_size",
			"part_count",
			"status",
			"expires_at",
			"created_at",
			"updated_at",
		},
		"file_upload_parts": {
			"session_id",
			"part_number",
			"size",
			"etag",
			"confirmed_at",
		},
		"products": {
			"id",
			"name",
			"sku",
			"price_cents",
			"status",
			"created_at",
			"updated_at",
		},
	} {
		got := bundledSQLiteColumns(t, db, table)
		for _, column := range columns {
			if !got[column] {
				t.Errorf("%s is missing column %q", table, column)
			}
		}
	}
	for _, table := range []string{
		"aginex_files_migrations",
		"aginex_starter_migrations",
	} {
		if !bundledSQLiteTableExists(t, db, table) {
			t.Errorf("migration history table %q was not created", table)
		}
	}
}

func TestBundledSQLiteFileUploadSessionSchemaEnforcesIdentityAndParts(
	t *testing.T,
) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(t.Context(), db, "sqlite", FilesModule()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users (
			id, email, display_name, password_hash, status, created_at, updated_at
		) VALUES (
			'upload-owner', 'upload@example.com', 'Upload', 'hash', 'active',
			'2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		);
		INSERT INTO file_objects (
			id, provider, object_key, original_name, content_type, size, owner_id,
			status, created_at, updated_at
		) VALUES
			('upload-file', 'local', 'uploads/file.bin', 'file.bin',
				 'application/octet-stream', 67108864, 'upload-owner', 'pending',
			 '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'),
			('upload-file-2', 'local', 'uploads/file-2.bin', 'file-2.bin',
				 'application/octet-stream', 67108864, 'upload-owner', 'pending',
			 '2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z');
		INSERT INTO file_upload_sessions (
			id, file_id, provider_upload_id, resume_fingerprint, part_size,
			part_count, status, expires_at, created_at, updated_at
		) VALUES (
			'upload-session', 'upload-file', 'provider-upload',
			'0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
			33554432, 2, 'active', '2026-08-12T00:00:00Z',
			'2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		);
		INSERT INTO file_upload_parts (
			session_id, part_number, size, etag, confirmed_at
		) VALUES (
			'upload-session', 1, 33554432, 'provider-etag',
			'2026-08-11T00:01:00Z'
		);
	`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`
		INSERT INTO file_upload_sessions (
			id, file_id, provider_upload_id, resume_fingerprint, part_size,
			part_count, status, expires_at, created_at, updated_at
		) VALUES (
			'duplicate-session', 'upload-file', 'other-upload',
			'abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789',
			33554432, 2, 'active', '2026-08-12T00:00:00Z',
			'2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		)
	`); err == nil {
		t.Fatal("file_upload_sessions accepted a second session for one file")
	}
	if _, err := db.Exec(`
		INSERT INTO file_upload_sessions (
			id, file_id, provider_upload_id, resume_fingerprint, part_size,
			part_count, status, expires_at, created_at, updated_at
		) VALUES (
			'one-part-session', 'upload-file-2', 'one-part-upload',
			'abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789',
			33554432, 1, 'active', '2026-08-12T00:00:00Z',
			'2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		)
	`); err == nil {
		t.Fatal("file_upload_sessions accepted part_count below 2")
	}
	for _, partNumber := range []int{0, 33} {
		if _, err := db.Exec(`
			INSERT INTO file_upload_parts (
				session_id, part_number, size, etag, confirmed_at
			) VALUES (?, ?, 1, 'invalid', '2026-08-11T00:02:00Z')
		`, "upload-session", partNumber); err == nil {
			t.Fatalf("file_upload_parts accepted part number %d", partNumber)
		}
	}

	if _, err := db.Exec(
		"DELETE FROM file_upload_sessions WHERE id = ?",
		"upload-session",
	); err != nil {
		t.Fatal(err)
	}
	var parts int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM file_upload_parts WHERE session_id = ?",
		"upload-session",
	).Scan(&parts); err != nil {
		t.Fatal(err)
	}
	if parts != 0 {
		t.Fatalf("parts after session delete = %d, want 0", parts)
	}
}

func TestBundledSQLiteEnsureCurrentRequiresFileUploadMetadataTables(
	t *testing.T,
) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(t.Context(), db, "sqlite", FilesModule()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE file_upload_parts"); err != nil {
		t.Fatal(err)
	}
	err := EnsureModulesCurrent(t.Context(), db, "sqlite", FilesModule())
	if err == nil || !strings.Contains(err.Error(), "file_upload_parts") {
		t.Fatalf("missing upload parts schema error = %v", err)
	}
}

func TestBundledSQLiteFileMigrationDownDropsTablesInDependencyOrder(t *testing.T) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(t.Context(), db, "sqlite", FilesModule()); err != nil {
		t.Fatal(err)
	}
	spec, err := bundledMigrationSpecification(bundledFiles)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newBundledMigrationProvider(db, "sqlite", spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{
		"file_objects",
		"file_upload_sessions",
		"file_upload_parts",
	} {
		if bundledSQLiteTableExists(t, db, table) {
			t.Fatalf("Files down retained %s", table)
		}
	}
}

func TestBundledSQLiteFileMigrationDownRejectsNonTerminalUploadSessions(t *testing.T) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(t.Context(), db, "sqlite", FilesModule()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users (
			id, email, display_name, password_hash, status, created_at, updated_at
		) VALUES (
			'down-owner', 'down@example.com', 'Down', 'hash', 'active',
			'2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		);
		INSERT INTO file_objects (
			id, provider, object_key, original_name, content_type, size, owner_id,
			status, created_at, updated_at
		) VALUES (
			'down-file', 'local', 'uploads/down-file', 'down.bin',
			'application/octet-stream', 67108864, 'down-owner', 'pending',
			'2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		);
		INSERT INTO file_upload_sessions (
			id, file_id, provider_upload_id, resume_fingerprint, part_size,
			part_count, status, expires_at, created_at, updated_at
		) VALUES (
			'down-session', 'down-file', 'provider-down',
			'0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef',
			33554432, 2, 'active', '2026-08-12T00:00:00Z',
			'2026-08-11T00:00:00Z', '2026-08-11T00:00:00Z'
		);
	`); err != nil {
		t.Fatal(err)
	}
	spec, err := bundledMigrationSpecification(bundledFiles)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newBundledMigrationProvider(db, "sqlite", spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 0); err == nil {
		t.Fatal("Files down accepted a non-terminal resumable upload")
	}
	for _, table := range []string{"file_objects", "file_upload_sessions", "file_upload_parts"} {
		if !bundledSQLiteTableExists(t, db, table) {
			t.Fatalf("failed guarded Files down removed %s", table)
		}
	}
}

func TestBundledSQLiteEnsureCurrentIsReadOnly(t *testing.T) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}

	if err := EnsureModulesCurrent(
		t.Context(),
		db,
		"sqlite",
		FilesModule(),
		StarterExampleModule(),
	); err == nil {
		t.Fatal("EnsureModulesCurrent succeeded before bundled migrations")
	}
	for _, table := range []string{
		"file_objects",
		"file_upload_sessions",
		"file_upload_parts",
		"products",
		"aginex_files_migrations",
		"aginex_starter_migrations",
	} {
		if bundledSQLiteTableExists(t, db, table) {
			t.Errorf("read-only schema check created table %q", table)
		}
	}

	if err := MigrateModulesUp(
		t.Context(),
		db,
		"sqlite",
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatal(err)
	}
	if err := EnsureModulesCurrent(
		t.Context(),
		db,
		"sqlite",
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatalf("EnsureModulesCurrent after migration: %v", err)
	}
}

func openBundledMigrationSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(
		"sqlite",
		filepath.Join(t.TempDir(), "bundled-migrations.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close bundled migration database: %v", err)
		}
	})
	return db
}

func bundledSQLiteTableExists(
	t *testing.T,
	db *sql.DB,
	table string,
) bool {
	t.Helper()
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count > 0
}

func bundledSQLiteColumns(
	t *testing.T,
	db *sql.DB,
	table string,
) map[string]bool {
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
		if err := rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return columns
}
