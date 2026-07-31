package app

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	coremigrate "github.com/xgtian-root/aginex/internal/platform/migrate"
)

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

func TestBundledSQLiteMigrationsAdoptPreReleaseCoreTablesWithoutDataLoss(
	t *testing.T,
) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE file_objects (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			bucket TEXT NOT NULL DEFAULT '',
			object_key TEXT NOT NULL UNIQUE,
			original_name TEXT NOT NULL,
			content_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			etag TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL REFERENCES users(id),
			visibility TEXT NOT NULL DEFAULT 'private',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE products (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			sku TEXT NOT NULL UNIQUE,
			price_cents INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'draft',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users (
			id, email, display_name, password_hash, status, created_at, updated_at
		) VALUES (
			'user-1', 'owner@example.com', 'Owner', 'hash', 'active',
			'2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
		);
		INSERT INTO file_objects (
			id, provider, object_key, original_name, content_type, size, owner_id,
			status, created_at, updated_at
		) VALUES (
			'file-1', 'local', 'legacy/file.png', 'file.png', 'image/png', 12,
			'user-1', 'ready', '2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
		);
		INSERT INTO products (
			id, name, sku, price_cents, status, created_at, updated_at
		) VALUES (
			'product-1', 'Legacy product', 'LEGACY-1', 1200, 'active',
			'2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
		);
	`); err != nil {
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
	if err := EnsureModulesCurrent(
		t.Context(),
		db,
		"sqlite",
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatalf("ensure adopted module schema current: %v", err)
	}
	for _, migration := range []bundledMigration{
		bundledFiles,
		bundledStarter,
	} {
		spec, err := bundledMigrationSpecification(migration)
		if err != nil {
			t.Fatal(err)
		}
		provider, err := newBundledMigrationProvider(
			t.Context(),
			db,
			"sqlite",
			spec,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.DownTo(t.Context(), 0); err != nil {
			t.Fatal(err)
		}
	}
	columns := bundledSQLiteColumns(t, db, "file_objects")
	for _, column := range []string{
		"sha256",
		"width",
		"height",
		"deleted_at",
	} {
		if !columns[column] {
			t.Errorf("adopted file_objects is missing column %q", column)
		}
	}

	var objectKey string
	if err := db.QueryRow(
		"SELECT object_key FROM file_objects WHERE id = ?",
		"file-1",
	).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	if objectKey != "legacy/file.png" {
		t.Fatalf("adopted file object key = %q", objectKey)
	}
	var sku string
	if err := db.QueryRow(
		"SELECT sku FROM products WHERE id = ?",
		"product-1",
	).Scan(&sku); err != nil {
		t.Fatal(err)
	}
	if sku != "LEGACY-1" {
		t.Fatalf("adopted product SKU = %q", sku)
	}
}

func TestBundledSQLiteMigrationsAdoptAlreadyVerifiedFileTable(
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
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO users (
			id, email, display_name, password_hash, status, created_at, updated_at
		) VALUES (
			'user-verified', 'verified@example.com', 'Verified', 'hash', 'active',
			'2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
		);
		INSERT INTO file_objects (
			id, provider, object_key, original_name, content_type, size, owner_id,
			status, created_at, updated_at
		) VALUES (
			'file-verified', 'local', 'verified/file.png', 'file.png',
			'image/png', 12, 'user-verified', 'ready',
			'2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
		);
		DROP TABLE aginex_files_migrations;
	`); err != nil {
		t.Fatal(err)
	}

	if err := MigrateModulesUp(
		t.Context(),
		db,
		"sqlite",
		FilesModule(),
	); err != nil {
		t.Fatal(err)
	}
	var objectKey string
	if err := db.QueryRow(
		"SELECT object_key FROM file_objects WHERE id = ?",
		"file-verified",
	).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	if objectKey != "verified/file.png" {
		t.Fatalf("re-adopted file object key = %q", objectKey)
	}
}

func TestBundledSQLiteMigrationRejectsPartialLegacyFileSchema(
	t *testing.T,
) {
	db := openBundledMigrationSQLite(t)
	if err := coremigrate.Up(db, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE file_objects (
			id TEXT PRIMARY KEY,
			provider TEXT NOT NULL,
			bucket TEXT NOT NULL DEFAULT '',
			object_key TEXT NOT NULL UNIQUE,
			original_name TEXT NOT NULL,
			content_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			etag TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL REFERENCES users(id),
			visibility TEXT NOT NULL DEFAULT 'private',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	err := MigrateModulesUp(
		t.Context(),
		db,
		"sqlite",
		FilesModule(),
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"cannot adopt partially upgraded file_objects table",
	) {
		t.Fatalf("partial legacy migration error = %v", err)
	}
	if bundledSQLiteTableExists(t, db, "aginex_files_migrations") {
		t.Fatal("failed partial adoption created migration history")
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
