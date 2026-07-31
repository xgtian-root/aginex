package app

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/xgtian-root/aginex/framework/module"
	coremigrate "github.com/xgtian-root/aginex/internal/platform/migrate"
)

func TestBundledModuleMigrationMatrix(t *testing.T) {
	tests := []struct {
		name       string
		driver     string
		sqlDriver  string
		dsn        string
		dialect    module.Dialect
		legacyDDL  []string
		insertData []string
	}{
		{
			name:      "PostgreSQL",
			driver:    "postgres",
			sqlDriver: "pgx",
			dsn:       os.Getenv("AGINEX_TEST_POSTGRES_DSN"),
			dialect:   module.DialectPostgreSQL,
			legacyDDL: []string{
				`CREATE TABLE file_objects (
					id UUID PRIMARY KEY,
					provider VARCHAR(32) NOT NULL,
					bucket VARCHAR(240) NOT NULL DEFAULT '',
					object_key VARCHAR(700) NOT NULL UNIQUE,
					original_name VARCHAR(500) NOT NULL,
					content_type VARCHAR(160) NOT NULL,
					size BIGINT NOT NULL CHECK (size > 0),
					etag VARCHAR(240) NOT NULL DEFAULT '',
					owner_id UUID NOT NULL REFERENCES users(id),
					visibility VARCHAR(32) NOT NULL DEFAULT 'private',
					status VARCHAR(32) NOT NULL DEFAULT 'pending',
					created_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL
				)`,
				`CREATE INDEX idx_file_objects_owner_id
					ON file_objects(owner_id)`,
				`CREATE TABLE products (
					id UUID PRIMARY KEY,
					name VARCHAR(240) NOT NULL,
					sku VARCHAR(120) NOT NULL UNIQUE,
					price_cents BIGINT NOT NULL DEFAULT 0
						CHECK (price_cents >= 0),
					status VARCHAR(32) NOT NULL DEFAULT 'draft',
					created_at TIMESTAMPTZ NOT NULL,
					updated_at TIMESTAMPTZ NOT NULL
				)`,
			},
			insertData: []string{
				`INSERT INTO users (
					id, email, display_name, password_hash, status,
					created_at, updated_at
				) VALUES (
					'11111111-1111-4111-8111-111111111111',
					'owner@example.com', 'Owner', 'hash', 'active',
					'2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
				)`,
				`INSERT INTO file_objects (
					id, provider, object_key, original_name, content_type,
					size, owner_id, status, created_at, updated_at
				) VALUES (
					'22222222-2222-4222-8222-222222222222',
					'local', 'legacy/file.png', 'file.png', 'image/png',
					12, '11111111-1111-4111-8111-111111111111', 'ready',
					'2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
				)`,
				`INSERT INTO products (
					id, name, sku, price_cents, status, created_at, updated_at
				) VALUES (
					'33333333-3333-4333-8333-333333333333',
					'Legacy product', 'LEGACY-1', 1200, 'active',
					'2026-07-31T00:00:00Z', '2026-07-31T00:00:00Z'
				)`,
			},
		},
		{
			name:      "MySQL",
			driver:    "mysql",
			sqlDriver: "mysql",
			dsn:       os.Getenv("AGINEX_TEST_MYSQL_DSN"),
			dialect:   module.DialectMySQL,
			legacyDDL: []string{
				`CREATE TABLE file_objects (
					id CHAR(36) PRIMARY KEY,
					provider VARCHAR(32) NOT NULL,
					bucket VARCHAR(240) NOT NULL DEFAULT '',
					object_key VARCHAR(700) NOT NULL UNIQUE,
					original_name VARCHAR(500) NOT NULL,
					content_type VARCHAR(160) NOT NULL,
					size BIGINT NOT NULL,
					etag VARCHAR(240) NOT NULL DEFAULT '',
					owner_id CHAR(36) NOT NULL,
					visibility VARCHAR(32) NOT NULL DEFAULT 'private',
					status VARCHAR(32) NOT NULL DEFAULT 'pending',
					created_at DATETIME(6) NOT NULL,
					updated_at DATETIME(6) NOT NULL,
					INDEX idx_file_objects_owner_id (owner_id),
					CONSTRAINT fk_file_objects_owner
						FOREIGN KEY (owner_id) REFERENCES users(id)
				) ENGINE=InnoDB`,
				`CREATE TABLE products (
					id CHAR(36) PRIMARY KEY,
					name VARCHAR(240) NOT NULL,
					sku VARCHAR(120) NOT NULL UNIQUE,
					price_cents BIGINT NOT NULL DEFAULT 0,
					status VARCHAR(32) NOT NULL DEFAULT 'draft',
					created_at DATETIME(6) NOT NULL,
					updated_at DATETIME(6) NOT NULL
				) ENGINE=InnoDB`,
			},
			insertData: []string{
				`INSERT INTO users (
					id, email, display_name, password_hash, status,
					created_at, updated_at
				) VALUES (
					'11111111-1111-4111-8111-111111111111',
					'owner@example.com', 'Owner', 'hash', 'active',
					'2026-07-31 00:00:00', '2026-07-31 00:00:00'
				)`,
				`INSERT INTO file_objects (
					id, provider, object_key, original_name, content_type,
					size, owner_id, status, created_at, updated_at
				) VALUES (
					'22222222-2222-4222-8222-222222222222',
					'local', 'legacy/file.png', 'file.png', 'image/png',
					12, '11111111-1111-4111-8111-111111111111', 'ready',
					'2026-07-31 00:00:00', '2026-07-31 00:00:00'
				)`,
				`INSERT INTO products (
					id, name, sku, price_cents, status, created_at, updated_at
				) VALUES (
					'33333333-3333-4333-8333-333333333333',
					'Legacy product', 'LEGACY-1', 1200, 'active',
					'2026-07-31 00:00:00', '2026-07-31 00:00:00'
				)`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.dsn == "" {
				t.Skip("integration DSN is not configured")
			}
			db, err := sql.Open(test.sqlDriver, test.dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.PingContext(t.Context()); err != nil {
				t.Fatal(err)
			}
			resetBundledIntegrationDatabase(t, db, test.driver)
			t.Cleanup(func() {
				resetBundledIntegrationDatabase(t, db, test.driver)
			})

			t.Run("fresh", func(t *testing.T) {
				if err := coremigrate.Up(db, test.driver); err != nil {
					t.Fatal(err)
				}
				for _, table := range []string{"products", "file_objects"} {
					exists, err := bundledTableExists(
						t.Context(),
						db,
						test.dialect,
						table,
					)
					if err != nil {
						t.Fatal(err)
					}
					if exists {
						t.Fatalf(
							"fresh core created business table %q",
							table,
						)
					}
				}
				if err := MigrateModulesUp(
					t.Context(),
					db,
					test.driver,
					FilesModule(),
					StarterExampleModule(),
				); err != nil {
					t.Fatal(err)
				}
				if err := EnsureModulesCurrent(
					t.Context(),
					db,
					test.driver,
					FilesModule(),
					StarterExampleModule(),
				); err != nil {
					t.Fatal(err)
				}
				resetBundledIntegrationDatabase(t, db, test.driver)
			})

			t.Run("legacy-adoption", func(t *testing.T) {
				if err := coremigrate.Up(db, test.driver); err != nil {
					t.Fatal(err)
				}
				execBundledIntegrationSQL(t, db, test.legacyDDL)
				execBundledIntegrationSQL(t, db, test.insertData)
				if err := MigrateModulesUp(
					t.Context(),
					db,
					test.driver,
					FilesModule(),
					StarterExampleModule(),
				); err != nil {
					t.Fatal(err)
				}
				if err := EnsureModulesCurrent(
					t.Context(),
					db,
					test.driver,
					FilesModule(),
					StarterExampleModule(),
				); err != nil {
					t.Fatal(err)
				}
				columns, err := bundledTableColumns(
					t.Context(),
					db,
					test.dialect,
					"file_objects",
				)
				if err != nil {
					t.Fatal(err)
				}
				for _, column := range []string{
					"sha256",
					"width",
					"height",
					"deleted_at",
				} {
					if _, ok := columns[column]; !ok {
						t.Errorf(
							"adopted file_objects is missing %q",
							column,
						)
					}
				}
				var objectKey string
				if err := db.QueryRowContext(
					t.Context(),
					`SELECT object_key FROM file_objects
					 WHERE object_key = 'legacy/file.png'`,
				).Scan(&objectKey); err != nil {
					t.Fatal(err)
				}
				if objectKey != "legacy/file.png" {
					t.Fatalf("adopted object key = %q", objectKey)
				}
				var sku string
				if err := db.QueryRowContext(
					t.Context(),
					`SELECT sku FROM products
					 WHERE sku = 'LEGACY-1'`,
				).Scan(&sku); err != nil {
					t.Fatal(err)
				}
				if sku != "LEGACY-1" {
					t.Fatalf("adopted product SKU = %q", sku)
				}
			})
		})
	}
}

func execBundledIntegrationSQL(
	t *testing.T,
	db *sql.DB,
	statements []string,
) {
	t.Helper()
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("execute integration SQL: %v", err)
		}
	}
}

func resetBundledIntegrationDatabase(
	t *testing.T,
	db *sql.DB,
	driver string,
) {
	t.Helper()
	tables := []string{
		"file_objects",
		"products",
		"user_identities",
		"sessions",
		"role_permissions",
		"user_roles",
		"permissions",
		"roles",
		"audit_logs",
		"users",
		"aginex_files_migrations",
		"aginex_starter_migrations",
		"aginex_rate_limit_windows",
		"aginex_rate_limit_migrations",
		"goose_db_version",
	}
	switch driver {
	case "postgres":
		for _, table := range tables {
			if _, err := db.ExecContext(
				t.Context(),
				"DROP TABLE IF EXISTS "+table+" CASCADE",
			); err != nil {
				t.Fatalf("reset postgres table %s: %v", table, err)
			}
		}
	case "mysql":
		for _, table := range tables {
			if _, err := db.ExecContext(
				t.Context(),
				"DROP TABLE IF EXISTS "+table,
			); err != nil {
				t.Fatalf("reset mysql table %s: %v", table, err)
			}
		}
	default:
		t.Fatalf("unsupported integration driver %q", driver)
	}
}
