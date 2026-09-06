package idempotency

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrationProviderUsesDedicatedHistoryAndSupportsRollback(t *testing.T) {
	db := openTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		MigrationTableName,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration history table count = %d, want 1", count)
	}
	status, err := Status(context.Background(), sqlDB, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if !status.IsCurrent() || status.Current != 1 || status.Latest != 1 {
		t.Fatalf("migration status = %#v, want current", status)
	}

	provider, err := NewMigrationProvider(sqlDB, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		TableName,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("state table count after rollback = %d, want 0", count)
	}
	if err := EnsureCurrent(context.Background(), sqlDB, "sqlite"); !errors.Is(err, ErrSchemaNotCurrent) {
		t.Fatalf("EnsureCurrent after rollback error = %v", err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationStatusDoesNotMutateEmptyDatabase(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(t.TempDir()+"/empty-idempotency.db"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	status, err := Status(context.Background(), sqlDB, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if status.Current != 0 || status.Latest != 1 || status.Pending != 1 || status.IsCurrent() {
		t.Fatalf("empty migration status = %#v", status)
	}
	if err := EnsureCurrent(context.Background(), sqlDB, "sqlite"); !errors.Is(err, ErrSchemaNotCurrent) {
		t.Fatalf("EnsureCurrent error = %v", err)
	}
	var count int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN (?, ?)",
		MigrationTableName,
		TableName,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("read-only status created %d tables", count)
	}
}

func TestMigrationProviderRejectsUnsupportedDialect(t *testing.T) {
	db := openTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMigrationProvider(sqlDB, "oracle"); !errors.Is(err, ErrUnsupportedDialect) {
		t.Fatalf("error = %v, want ErrUnsupportedDialect", err)
	}
	if _, err := NewMigrationProvider(nil, "sqlite"); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("nil database error = %v, want ErrDatabaseRequired", err)
	}
}

func TestGORMProviderDatabaseMatrix(t *testing.T) {
	cases := []struct {
		name      string
		dialect   string
		dsn       string
		dialector func(string) gorm.Dialector
	}{
		{
			name:      "PostgreSQL",
			dialect:   "postgres",
			dsn:       os.Getenv("AGINEX_TEST_POSTGRES_DSN"),
			dialector: func(dsn string) gorm.Dialector { return gormpostgres.Open(dsn) },
		},
		{
			name:      "MySQL",
			dialect:   "mysql",
			dsn:       os.Getenv("AGINEX_TEST_MYSQL_DSN"),
			dialector: func(dsn string) gorm.Dialector { return gormmysql.Open(dsn) },
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if item.dsn == "" {
				t.Skip("integration DSN is not configured")
			}
			db, err := gorm.Open(
				item.dialector(item.dsn),
				&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
			)
			if err != nil {
				t.Fatal(err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := sqlDB.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			})
			provider, err := NewMigrationProvider(sqlDB, item.dialect)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.Up(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := provider.DownTo(context.Background(), 0); err != nil {
					t.Errorf("roll back optional idempotency migrations: %v", err)
				}
			})
			exerciseDatabaseConcurrency(t, db)
		})
	}
}

func exerciseDatabaseConcurrency(t *testing.T, db *gorm.DB) {
	t.Helper()
	const callers = 24
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	first := newTestStore(t, db, clock)
	second := newTestStore(t, db, clock)
	request := testClaimRequest()
	request.Key = t.Name()
	start := make(chan struct{})
	results := make(chan ClaimResult, callers)
	errs := make(chan error, callers)
	var group sync.WaitGroup
	for index := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			current := first
			if index%2 == 0 {
				current = second
			}
			result, err := current.Claim(context.Background(), request)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("claim: %v", err)
	}
	var execution Lease
	executions := 0
	for result := range results {
		if result.Disposition == DispositionExecute {
			executions++
			execution = result.Lease
		}
	}
	if executions != 1 {
		t.Fatalf("execution claims = %d, want 1", executions)
	}
	if _, err := first.Complete(
		context.Background(),
		execution,
		testResponse(http.StatusCreated),
	); err != nil {
		t.Fatal(err)
	}
	replay, err := second.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Disposition != DispositionReplay {
		t.Fatalf("post-completion claim = %+v, want replay", replay)
	}
}
