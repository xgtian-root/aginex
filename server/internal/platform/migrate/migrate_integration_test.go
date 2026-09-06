package migrate

import (
	"database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestDatabaseMigrationMatrix(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		sql    string
		dsn    string
	}{
		{name: "PostgreSQL", driver: "postgres", sql: "pgx", dsn: os.Getenv("AGINEX_TEST_POSTGRES_DSN")},
		{name: "MySQL", driver: "mysql", sql: "mysql", dsn: os.Getenv("AGINEX_TEST_MYSQL_DSN")},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if item.dsn == "" {
				t.Skip("integration DSN is not configured")
			}
			db, err := sql.Open(item.sql, item.dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := db.Ping(); err != nil {
				t.Fatal(err)
			}
			if err := Up(db, item.driver); err != nil {
				t.Fatal(err)
			}
			assertAuditActorIdentifierWidth(t, db, item.driver)
			if err := DownToZero(db, item.driver); err != nil {
				t.Fatal(err)
			}
			if err := Up(db, item.driver); err != nil {
				t.Fatal(err)
			}
			assertAuditActorIdentifierWidth(t, db, item.driver)
		})
	}
}

func assertAuditActorIdentifierWidth(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	var (
		query string
		width sql.NullInt64
	)
	switch driver {
	case "postgres":
		query = `
			SELECT character_maximum_length
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'audit_logs'
			  AND column_name = 'actor_id'
		`
	case "mysql":
		query = `
			SELECT character_maximum_length
			FROM information_schema.columns
			WHERE table_schema = DATABASE()
			  AND table_name = 'audit_logs'
			  AND column_name = 'actor_id'
		`
	default:
		t.Fatalf("unsupported driver %q", driver)
	}
	if err := db.QueryRow(query).Scan(&width); err != nil {
		t.Fatal(err)
	}
	if !width.Valid || width.Int64 != 160 {
		t.Fatalf("audit_logs.actor_id width = %#v, want 160", width)
	}
}
