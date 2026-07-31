package tokenauth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestConcurrentRefreshAllowsOneWinnerAndReplayRevokesFamily(t *testing.T) {
	ctx := context.Background()
	clock := newFakeClock(time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC))
	db := openTokenDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// The single SQLite connection removes driver-specific busy errors while
	// retaining concurrent callers and the database compare-and-set boundary.
	// Live PostgreSQL/MySQL tests exercise the same implementation with pools.
	sqlDB.SetMaxOpenConns(1)
	service := newTestService(t, db, clock, newDeterministicReader(1))

	initial, err := service.Issue(ctx, "user-1", Device{
		ID:       "device-1",
		Name:     "Alice's phone",
		Platform: "ios",
		Metadata: map[string]string{"appVersion": "1.0.0"},
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan TokenPair, 2)
	errs := make(chan error, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			pair, refreshErr := service.Refresh(ctx, initial.RefreshToken.Value)
			if refreshErr != nil {
				errs <- refreshErr
				return
			}
			results <- pair
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)

	var winners []TokenPair
	for pair := range results {
		winners = append(winners, pair)
	}
	replays := 0
	for refreshErr := range errs {
		if errors.Is(refreshErr, ErrRefreshReplay) {
			replays++
			continue
		}
		t.Errorf("concurrent refresh error = %v, want ErrRefreshReplay", refreshErr)
	}
	if len(winners) != 1 || replays != 1 {
		t.Fatalf("winners=%d replays=%d, want one of each", len(winners), replays)
	}

	if _, err := service.Refresh(ctx, winners[0].RefreshToken.Value); !errors.Is(err, ErrRefreshRevoked) {
		t.Fatalf("winner token after replay error = %v, want ErrRefreshRevoked", err)
	}
	var active int64
	if err := db.Table(RefreshTokenTableName).
		Where("family_id = ? AND status = ?", initial.FamilyID, RefreshStatusActive).
		Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatalf("active tokens in replayed family = %d, want 0", active)
	}
}

func TestConcurrentRefreshAndRevokeUserLeaveNoActiveChild(
	t *testing.T,
) {
	ctx := context.Background()
	clock := newFakeClock(
		time.Date(2026, 7, 31, 13, 0, 0, 0, time.UTC),
	)
	db := openTokenDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	service := newTestService(
		t,
		db,
		clock,
		newDeterministicReader(91),
	)
	initial, err := service.Issue(
		ctx,
		"user-1",
		Device{ID: "revoke-race-device"},
	)
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	refreshResult := make(chan error, 1)
	revokeResult := make(chan error, 1)
	go func() {
		<-start
		_, refreshErr := service.Refresh(
			ctx,
			initial.RefreshToken.Value,
		)
		refreshResult <- refreshErr
	}()
	go func() {
		<-start
		_, revokeErr := service.RevokeUser(ctx, "user-1")
		revokeResult <- revokeErr
	}()
	close(start)
	refreshErr := <-refreshResult
	revokeErr := <-revokeResult
	if refreshErr != nil &&
		!errors.Is(refreshErr, ErrRefreshRevoked) {
		t.Fatalf("refresh/revoke race error = %v", refreshErr)
	}
	if revokeErr != nil {
		t.Fatalf("revoke/refresh race error = %v", revokeErr)
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
			"active refresh tokens after revoke race = %d, want 0",
			active,
		)
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}

type deterministicReader struct {
	mu   sync.Mutex
	next byte
}

func newDeterministicReader(seed byte) *deterministicReader {
	return &deterministicReader{next: seed}
}

func (reader *deterministicReader) Read(target []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	for index := range target {
		target[index] = reader.next
		reader.next++
	}
	return len(target), nil
}

type staticSubjects struct {
	mu       sync.Mutex
	subjects map[string]Subject
}

func (subjects *staticSubjects) LookupSubject(_ context.Context, userID string) (Subject, error) {
	subjects.mu.Lock()
	defer subjects.mu.Unlock()
	subject, ok := subjects.subjects[userID]
	if !ok {
		return Subject{}, ErrSubjectNotFound
	}
	return subject, nil
}

func newTestService(t *testing.T, db *gorm.DB, clock Clock, random *deterministicReader) *Service {
	t.Helper()
	store, err := NewGORMStore(db)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Config{
		Issuer:           "https://issuer.example.test",
		Audience:         "posta-api",
		AccessTTL:        5 * time.Minute,
		RefreshTTL:       30 * 24 * time.Hour,
		AccessSigningKey: []byte("access-signing-key-with-at-least-32-bytes"),
		RefreshHashKey:   []byte("refresh-hashing-key-with-at-least-32-bytes"),
	}, Dependencies{
		Store:    store,
		Subjects: &staticSubjects{subjects: map[string]Subject{"user-1": {ID: "user-1", Active: true}}},
		Clock:    clock,
		Random:   random,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func openTokenDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(t.TempDir()+"/tokenauth.db"),
		&gorm.Config{Logger: logger.Default.LogMode(logger.Silent)},
	)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := NewMigrationProvider(sqlDB, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}
