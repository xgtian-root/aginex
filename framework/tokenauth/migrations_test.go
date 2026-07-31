package tokenauth

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	gormmysql "gorm.io/driver/mysql"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestMigrationProviderUsesDedicatedHistorySupportsRollbackAndHasNoRawTokenColumn(t *testing.T) {
	db := openTokenDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	status, err := Status(context.Background(), sqlDB, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if !status.IsCurrent() || status.Current != 2 || status.Latest != 2 {
		t.Fatalf("migration status = %+v, want current version 2", status)
	}

	type column struct {
		Name string
	}
	var columns []column
	if err := db.Raw("PRAGMA table_info(" + RefreshTokenTableName + ")").Scan(&columns).Error; err != nil {
		t.Fatal(err)
	}
	if len(columns) == 0 {
		t.Fatal("refresh token migration created no columns")
	}
	for _, current := range columns {
		switch strings.ToLower(current.Name) {
		case "token", "refresh_token", "value", "secret", "raw_token":
			t.Fatalf("migration exposes forbidden plaintext token column %q", current.Name)
		}
	}

	var coreHistory int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		"goose_db_version",
	).Scan(&coreHistory).Error; err != nil {
		t.Fatal(err)
	}
	if coreHistory != 0 {
		t.Fatal("optional token migration mutated the application's core Goose history")
	}

	provider, err := NewMigrationProvider(sqlDB, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCurrent(context.Background(), sqlDB, "sqlite"); !errors.Is(err, ErrSchemaNotCurrent) {
		t.Fatalf("EnsureCurrent after rollback error = %v", err)
	}
	var tableCount int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
		RefreshTokenTableName,
	).Scan(&tableCount).Error; err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("refresh table count after rollback = %d, want 0", tableCount)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationStatusDoesNotMutateEmptyDatabase(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open(t.TempDir()+"/empty-tokenauth.db"),
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
	if status.Current != 0 || status.Latest != 2 || status.Pending != 2 || status.IsCurrent() {
		t.Fatalf("empty migration status = %+v", status)
	}
	if err := EnsureCurrent(context.Background(), sqlDB, "sqlite"); !errors.Is(err, ErrSchemaNotCurrent) {
		t.Fatalf("EnsureCurrent error = %v", err)
	}
	var count int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN (?, ?)",
		MigrationTableName,
		RefreshTokenTableName,
	).Scan(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("read-only status created %d tables", count)
	}
}

func TestMigrationProviderRejectsUnsupportedDialect(t *testing.T) {
	db := openTokenDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewMigrationProvider(nil, "sqlite"); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("nil database error = %v, want ErrDatabaseRequired", err)
	}
	if _, err := NewMigrationProvider(sqlDB, "oracle"); !errors.Is(err, ErrUnsupportedDialect) {
		t.Fatalf("unsupported dialect error = %v, want ErrUnsupportedDialect", err)
	}
	if _, err := Status(context.Background(), sqlDB, "oracle"); !errors.Is(err, ErrUnsupportedDialect) {
		t.Fatalf("unsupported status dialect error = %v, want ErrUnsupportedDialect", err)
	}
}

func TestTokenAuthDatabaseMatrix(t *testing.T) {
	cases := []struct {
		name      string
		dialect   string
		env       string
		dialector func(string) gorm.Dialector
	}{
		{
			name:      "PostgreSQL",
			dialect:   "postgres",
			env:       "AGINEX_TEST_POSTGRES_DSN",
			dialector: func(dsn string) gorm.Dialector { return gormpostgres.Open(dsn) },
		},
		{
			name:      "MySQL",
			dialect:   "mysql",
			env:       "AGINEX_TEST_MYSQL_DSN",
			dialector: func(dsn string) gorm.Dialector { return gormmysql.Open(dsn) },
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			dsn := os.Getenv(item.env)
			if dsn == "" {
				t.Skipf(
					"%s is not configured; %s token-auth migrations and atomic rotation were not verified locally",
					item.env,
					item.name,
				)
			}
			db, err := gorm.Open(
				item.dialector(dsn),
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
					t.Errorf("close %s database: %v", item.name, err)
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
					t.Errorf("roll back %s token-auth migrations: %v", item.name, err)
				}
			})
			if err := db.Exec("DELETE FROM " + RefreshTokenTableName).Error; err != nil {
				t.Fatal(err)
			}

			exerciseMatrixRotation(t, db)
			exerciseMatrixRevokeRace(t, db)
		})
	}
}

func exerciseMatrixRevokeRace(t *testing.T, db *gorm.DB) {
	t.Helper()
	clock := newFakeClock(
		time.Date(2026, 7, 31, 18, 30, 0, 0, time.UTC),
	)
	service := newTestService(
		t,
		db,
		clock,
		newDeterministicReader(220),
	)
	initial, err := service.Issue(
		context.Background(),
		"user-1",
		Device{ID: "matrix-race-device"},
	)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	refreshResult := make(chan error, 1)
	revokeResult := make(chan error, 1)
	go func() {
		<-start
		_, currentErr := service.Refresh(
			context.Background(),
			initial.RefreshToken.Value,
		)
		refreshResult <- currentErr
	}()
	go func() {
		<-start
		_, currentErr := service.RevokeUser(
			context.Background(),
			"user-1",
		)
		revokeResult <- currentErr
	}()
	close(start)
	refreshErr := <-refreshResult
	revokeErr := <-revokeResult
	if refreshErr != nil &&
		!errors.Is(refreshErr, ErrRefreshRevoked) {
		t.Fatalf("matrix refresh/revoke error = %v", refreshErr)
	}
	if revokeErr != nil {
		t.Fatalf("matrix revoke/refresh error = %v", revokeErr)
	}
	var active int64
	if err := db.Table(RefreshTokenTableName).
		Where(
			"user_id = ? AND status = ?",
			"user-1",
			RefreshStatusActive,
		).
		Count(&active).
		Error; err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf(
			"matrix active tokens after revoke race = %d",
			active,
		)
	}
}

func exerciseMatrixRotation(t *testing.T, db *gorm.DB) {
	t.Helper()
	clock := newFakeClock(time.Date(2026, 7, 31, 18, 0, 0, 0, time.UTC))
	service := newTestService(t, db, clock, newDeterministicReader(170))
	initial, err := service.Issue(context.Background(), "user-1", Device{ID: "matrix-device"})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := service.Refresh(context.Background(), initial.RefreshToken.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(context.Background(), initial.RefreshToken.Value); !errors.Is(err, ErrRefreshReplay) {
		t.Fatalf("matrix replay error = %v, want ErrRefreshReplay", err)
	}
	if _, err := service.Refresh(context.Background(), rotated.RefreshToken.Value); !errors.Is(err, ErrRefreshRevoked) {
		t.Fatalf("matrix family revocation error = %v, want ErrRefreshRevoked", err)
	}
}
