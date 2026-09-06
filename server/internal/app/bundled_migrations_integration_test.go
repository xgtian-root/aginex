package app

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/xgtian-root/aginex/server/framework/module"
	coremigrate "github.com/xgtian-root/aginex/server/internal/platform/migrate"
)

func TestBundledModuleMigrationMatrix(t *testing.T) {
	tests := []struct {
		name      string
		driver    string
		sqlDriver string
		dsn       string
		dialect   module.Dialect
	}{
		{
			name:      "PostgreSQL",
			driver:    "postgres",
			sqlDriver: "pgx",
			dsn:       os.Getenv("AGINEX_TEST_POSTGRES_DSN"),
			dialect:   module.DialectPostgreSQL,
		},
		{
			name:      "MySQL",
			driver:    "mysql",
			sqlDriver: "mysql",
			dsn:       os.Getenv("AGINEX_TEST_MYSQL_DSN"),
			dialect:   module.DialectMySQL,
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

			if err := coremigrate.Up(db, test.driver); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{
				"products",
				"file_upload_parts",
				"file_upload_sessions",
				"file_objects",
			} {
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
					t.Fatalf("fresh core created business table %q", table)
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
			for table, required := range map[string][]string{
				"file_objects": {
					"storage_profile_id",
					"sha256",
					"upload_expires_at",
					"deleted_at",
				},
				"file_upload_sessions": {
					"provider_upload_id",
					"resume_fingerprint",
					"part_count",
					"expires_at",
				},
				"file_upload_parts": {
					"session_id",
					"part_number",
					"etag",
				},
			} {
				columns, err := bundledTableColumns(
					t.Context(),
					db,
					test.dialect,
					table,
				)
				if err != nil {
					t.Fatal(err)
				}
				for _, column := range required {
					if _, ok := columns[column]; !ok {
						t.Errorf("%s is missing %q", table, column)
					}
				}
			}
		})
	}
}

func resetBundledIntegrationDatabase(
	t *testing.T,
	db *sql.DB,
	driver string,
) {
	t.Helper()
	tables := []string{
		"file_upload_parts",
		"file_upload_sessions",
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
