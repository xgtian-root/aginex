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
			if err := DownToZero(db, item.driver); err != nil {
				t.Fatal(err)
			}
			if err := Up(db, item.driver); err != nil {
				t.Fatal(err)
			}
		})
	}
}
