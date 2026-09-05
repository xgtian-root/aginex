package database

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/xgtian-root/aginex/internal/config"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const sqliteBusyTimeoutMilliseconds = 10_000

func Open(cfg config.Database) (*gorm.DB, error) {
	return OpenContext(context.Background(), cfg)
}

// OpenContext opens and verifies a database while honoring the caller's
// startup deadline. MySQL version discovery is disabled so the first network
// operation is the explicit context-aware ping below.
func OpenContext(ctx context.Context, cfg config.Database) (*gorm.DB, error) {
	if ctx == nil {
		return nil, fmt.Errorf("database context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite":
		if err := os.MkdirAll(filepath.Dir(cfg.DSN), 0o750); err != nil && filepath.Dir(cfg.DSN) != "." {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
		dsn, err := sqliteDSN(cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("configure sqlite connection: %w", err)
		}
		dialector = sqlite.Open(dsn)
	case "postgres":
		dialector = postgres.Open(cfg.DSN)
	case "mysql":
		dialector = mysql.New(mysql.Config{
			DSN:                       cfg.DSN,
			SkipInitializeWithVersion: true,
		})
	default:
		return nil, fmt.Errorf("unsupported database driver %q", cfg.Driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               newSafeLogger(slog.Default()),
		NowFunc: func() time.Time {
			return time.Now().UTC()
		},
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// sqliteDSN applies the write-concurrency policy to every connection opened by
// database/sql. WAL permits readers while a writer is active, busy_timeout
// waits for the current writer, and immediate transactions acquire the write
// reservation at Begin instead of failing later while upgrading a read lock.
func sqliteDSN(dsn string) (string, error) {
	path, rawQuery, _ := strings.Cut(dsn, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", err
	}

	pragmas := query["_pragma"][:0]
	for _, pragma := range query["_pragma"] {
		switch sqlitePragmaName(pragma) {
		case "busy_timeout", "journal_mode":
			continue
		default:
			pragmas = append(pragmas, pragma)
		}
	}
	query["_pragma"] = pragmas
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", sqliteBusyTimeoutMilliseconds))
	query.Add("_pragma", "journal_mode(WAL)")
	query.Set("_txlock", "immediate")

	return path + "?" + query.Encode(), nil
}

func sqlitePragmaName(pragma string) string {
	pragma = strings.TrimSpace(pragma)
	if end := strings.IndexAny(pragma, "(= \t\r\n"); end >= 0 {
		pragma = pragma[:end]
	}
	return strings.ToLower(pragma)
}
