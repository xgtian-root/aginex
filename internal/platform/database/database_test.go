package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/internal/config"
)

func TestOpenContextRejectsCanceledStartupBeforeOpening(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := OpenContext(ctx, config.Database{
		Driver: "sqlite",
		DSN:    t.TempDir() + "/must-not-open.db",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext error = %v, want context cancellation", err)
	}
}

func TestOpenSQLiteSerializesConcurrentWriteTransactions(t *testing.T) {
	db, err := OpenContext(t.Context(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "concurrent-writes.db"),
	})
	if err != nil {
		t.Fatalf("OpenContext() error = %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB() error = %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	if _, err := sqlDB.ExecContext(t.Context(), `CREATE TABLE writes (id INTEGER PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	conn, err := sqlDB.Conn(t.Context())
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	var journalMode string
	if err := conn.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		_ = conn.Close()
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		_ = conn.Close()
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var busyTimeout int
	if err := conn.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		_ = conn.Close()
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != sqliteBusyTimeoutMilliseconds {
		_ = conn.Close()
		t.Fatalf("busy_timeout = %d, want %d", busyTimeout, sqliteBusyTimeoutMilliseconds)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close connection: %v", err)
	}

	first, err := sqlDB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin first transaction: %v", err)
	}
	defer func() { _ = first.Rollback() }()
	if _, err := first.ExecContext(t.Context(), `INSERT INTO writes (id, value) VALUES (1, 'first')`); err != nil {
		t.Fatalf("insert first row: %v", err)
	}

	type beginResult struct {
		tx  *sql.Tx
		err error
	}
	secondStarted := make(chan struct{})
	secondResult := make(chan beginResult, 1)
	go func() {
		close(secondStarted)
		tx, err := sqlDB.BeginTx(context.Background(), nil)
		secondResult <- beginResult{tx: tx, err: err}
	}()
	<-secondStarted

	select {
	case result := <-secondResult:
		if result.tx != nil {
			_ = result.tx.Rollback()
		}
		t.Fatalf("second write transaction did not wait for the active writer: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := first.Commit(); err != nil {
		t.Fatalf("commit first transaction: %v", err)
	}

	var second beginResult
	select {
	case second = <-secondResult:
	case <-time.After(2 * time.Second):
		t.Fatal("second write transaction did not resume after the active writer committed")
	}
	if second.err != nil {
		t.Fatalf("begin second transaction: %v", second.err)
	}
	if second.tx == nil {
		t.Fatal("begin second transaction returned a nil transaction")
	}
	defer func() { _ = second.tx.Rollback() }()
	if _, err := second.tx.ExecContext(t.Context(), `INSERT INTO writes (id, value) VALUES (2, 'second')`); err != nil {
		t.Fatalf("insert second row: %v", err)
	}
	if err := second.tx.Commit(); err != nil {
		t.Fatalf("commit second transaction: %v", err)
	}

	var count int
	if err := sqlDB.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM writes`).Scan(&count); err != nil {
		t.Fatalf("count writes: %v", err)
	}
	if count != 2 {
		t.Fatalf("write count = %d, want 2", count)
	}
}

func TestSQLiteDSNPreservesOptionsAndEnforcesWritePolicy(t *testing.T) {
	dsn, err := sqliteDSN("file:test.db?cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(1)&_pragma=journal_mode(delete)&_txlock=deferred")
	if err != nil {
		t.Fatalf("sqliteDSN() error = %v", err)
	}
	wantParts := []string{
		"cache=shared",
		"_pragma=foreign_keys%281%29",
		fmt.Sprintf("_pragma=busy_timeout%%28%d%%29", sqliteBusyTimeoutMilliseconds),
		"_pragma=journal_mode%28WAL%29",
		"_txlock=immediate",
	}
	for _, want := range wantParts {
		if !containsQueryPart(dsn, want) {
			t.Errorf("sqliteDSN() = %q, missing %q", dsn, want)
		}
	}
	for _, unwanted := range []string{"busy_timeout%281%29", "journal_mode%28delete%29", "_txlock=deferred"} {
		if containsQueryPart(dsn, unwanted) {
			t.Errorf("sqliteDSN() = %q, retained conflicting option %q", dsn, unwanted)
		}
	}
}

func containsQueryPart(dsn, part string) bool {
	return strings.Contains(dsn, part)
}
