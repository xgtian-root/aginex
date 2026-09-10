package configcli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/glebarez/go-sqlite"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/xgtian-root/aginex/server/internal/config"
)

func databaseTarget(c config.Database) string {
	switch c.Driver {
	case "postgres":
		if p, e := pgx.ParseConfig(c.DSN); e == nil {
			return fmt.Sprintf("postgres host=%s port=%d database=%s", p.Host, p.Port, p.Database)
		}
	case "mysql":
		if p, e := mysql.ParseDSN(c.DSN); e == nil {
			return fmt.Sprintf("mysql address=%s database=%s", p.Addr, p.DBName)
		}
	case "sqlite":
		path, _, _ := strings.Cut(c.DSN, "?")
		return "sqlite " + path
	}
	return c.Driver + " [connection redacted]"
}
func testDatabase(ctx context.Context, c config.Database) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	driver, dsn := c.Driver, c.DSN
	switch driver {
	case "postgres":
		driver = "pgx"
		_, err := pgx.ParseConfig(dsn)
		if err != nil {
			return errors.New("postgres connection syntax is invalid")
		}
	case "mysql":
		p, err := mysql.ParseDSN(dsn)
		if err != nil {
			return errors.New("mysql connection syntax is invalid")
		}
		p.Timeout = 10 * time.Second
		p.ReadTimeout = 10 * time.Second
		p.WriteTimeout = 10 * time.Second
		dsn = p.FormatDSN()
	case "sqlite":
		raw, _, _ := strings.Cut(dsn, "?")
		raw = strings.TrimPrefix(raw, "file:")
		path, err := url.PathUnescape(raw)
		if err != nil {
			return errors.New("sqlite path is invalid")
		}
		if path == ":memory:" || path == "" {
			return errors.New("sqlite connection test requires an existing persistent database")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("sqlite target does not exist or is not a regular database file; no file was created")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return errors.New("sqlite path is invalid")
		}
		u := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}
		dsn = u.String()
	default:
		return errors.New("unsupported database type")
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return errors.New("database connection could not be opened (credentials omitted)")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		if ctx.Err() != nil {
			return errors.New("database connection test timed out or was cancelled")
		}
		return errors.New("database connection test failed; check host, database, credentials and TLS (driver details omitted)")
	}
	if c.Driver == "sqlite" {
		var count int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&count); err != nil {
			return errors.New("sqlite target is not a readable database")
		}
	}
	return nil
}
