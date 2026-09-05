package ratelimit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestConsumeFixedWindowAndRetryAfter(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 250_000_000, time.UTC))
	db := openTestDatabase(t)
	limiter := newTestLimiter(t, db, clock)
	request := Request{
		Namespace: "login.ip",
		Key:       "203.0.113.42",
		Limit:     2,
		Cost:      1,
		Window:    time.Second,
	}

	first, err := limiter.Consume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, first, true, 1, 0, time.Date(2026, 7, 31, 8, 0, 1, 0, time.UTC))

	second, err := limiter.Consume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, second, true, 0, 0, first.ResetAt)

	denied, err := limiter.Consume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, denied, false, 0, 750*time.Millisecond, first.ResetAt)

	clock.Advance(750 * time.Millisecond)
	reset, err := limiter.Consume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, reset, true, 1, 0, time.Date(2026, 7, 31, 8, 0, 2, 0, time.UTC))
}

func TestConsumeSupportsWeightedCosts(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC))
	limiter := newTestLimiter(t, openTestDatabase(t), clock)
	request := Request{
		Namespace: "uploads.user",
		Key:       "user-1",
		Limit:     10,
		Cost:      4,
		Window:    time.Minute,
	}

	first, err := limiter.Consume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Allowed || first.Remaining != 6 {
		t.Fatalf("first decision = %+v, want allowed with 6 remaining", first)
	}
	second, err := limiter.Consume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Allowed || second.Remaining != 2 {
		t.Fatalf("second decision = %+v, want allowed with 2 remaining", second)
	}
	third, err := limiter.Consume(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if third.Allowed || third.Remaining != 2 || third.RetryAfter != time.Minute {
		t.Fatalf("third decision = %+v, want denied without consuming capacity", third)
	}
}

func TestConcurrentConsumeNeverExceedsLimit(t *testing.T) {
	const (
		callers = 96
		limit   = 23
	)
	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// A single SQLite connection avoids driver-specific SQLITE_BUSY behavior
	// while still exercising interleaved optimistic reads and compare-and-swap
	// updates from many goroutines. PostgreSQL/MySQL run the same provider code.
	sqlDB.SetMaxOpenConns(1)

	firstLimiter := newTestLimiter(t, db, clock)
	secondLimiter := newTestLimiter(t, db, clock)
	request := Request{
		Namespace: "token.refresh",
		Key:       "family-1",
		Limit:     limit,
		Cost:      1,
		Window:    time.Minute,
	}
	start := make(chan struct{})
	decisions := make(chan Decision, callers)
	errs := make(chan error, callers)
	var group sync.WaitGroup

	for i := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			limiter := firstLimiter
			if i%2 == 0 {
				limiter = secondLimiter
			}
			decision, consumeErr := limiter.Consume(context.Background(), request)
			if consumeErr != nil {
				errs <- consumeErr
				return
			}
			decisions <- decision
		}()
	}
	close(start)
	group.Wait()
	close(decisions)
	close(errs)

	for consumeErr := range errs {
		t.Errorf("concurrent consume: %v", consumeErr)
	}
	allowed := 0
	for decision := range decisions {
		if decision.Allowed {
			allowed++
		}
	}
	if allowed != limit {
		t.Fatalf("allowed = %d, want %d", allowed, limit)
	}

	var row windowRecord
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.RequestCount != limit {
		t.Fatalf("stored request_count = %d, want %d", row.RequestCount, limit)
	}
}

func TestConsumeKeepsBucketsIndependentAndDoesNotPersistRawKey(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	limiter := newTestLimiter(t, db, clock)

	requests := []Request{
		{Namespace: "login.ip", Key: "sensitive@example.com", Limit: 1, Cost: 1, Window: time.Minute},
		{Namespace: "login.user", Key: "sensitive@example.com", Limit: 1, Cost: 1, Window: time.Minute},
		{Namespace: "login.ip", Key: "sensitive@example.com", Limit: 1, Cost: 1, Window: time.Hour},
	}
	for _, request := range requests {
		decision, err := limiter.Consume(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Allowed {
			t.Fatalf("independent bucket was unexpectedly denied: %+v", request)
		}
	}

	var rows []windowRecord
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(requests) {
		t.Fatalf("rows = %d, want %d", len(rows), len(requests))
	}
	for _, row := range rows {
		if len(row.KeyHash) != 64 {
			t.Fatalf("key hash length = %d, want 64", len(row.KeyHash))
		}
		if strings.Contains(row.KeyHash, "sensitive") {
			t.Fatalf("raw key leaked into persisted hash %q", row.KeyHash)
		}
	}
}

func TestCleanupExpiredIsBoundedAndPreservesActiveWindows(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	limiter := newTestLimiter(t, db, clock)

	for _, key := range []string{"expired-1", "expired-2", "expired-3"} {
		_, err := limiter.Consume(context.Background(), Request{
			Namespace: "cleanup",
			Key:       key,
			Limit:     1,
			Cost:      1,
			Window:    time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(2 * time.Second)
	if _, err := limiter.Consume(context.Background(), Request{
		Namespace: "cleanup",
		Key:       "active",
		Limit:     1,
		Cost:      1,
		Window:    time.Minute,
	}); err != nil {
		t.Fatal(err)
	}

	deleted, err := limiter.CleanupExpired(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("first cleanup deleted %d rows, want 2", deleted)
	}
	deleted, err = limiter.CleanupExpired(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("second cleanup deleted %d rows, want 1", deleted)
	}

	var rows []windowRecord
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ExpiresAtNS <= clock.Now().UnixNano() {
		t.Fatalf("cleanup retained rows = %+v, want one active row", rows)
	}
}

func TestConsumeRejectsInvalidRequestsBeforeDatabaseAccess(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	limiter := newTestLimiter(t, db, clock)
	valid := Request{
		Namespace: "login.ip",
		Key:       "203.0.113.42",
		Limit:     1,
		Cost:      1,
		Window:    time.Minute,
	}
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{name: "empty namespace", mutate: func(r *Request) { r.Namespace = "" }},
		{name: "uppercase namespace", mutate: func(r *Request) { r.Namespace = "Login.IP" }},
		{name: "long namespace", mutate: func(r *Request) { r.Namespace = "a" + strings.Repeat("b", MaxNamespaceBytes) }},
		{name: "empty key", mutate: func(r *Request) { r.Key = "" }},
		{name: "long key", mutate: func(r *Request) { r.Key = strings.Repeat("x", MaxKeyBytes+1) }},
		{name: "control character", mutate: func(r *Request) { r.Key = "key\nother" }},
		{name: "zero limit", mutate: func(r *Request) { r.Limit = 0 }},
		{name: "zero cost", mutate: func(r *Request) { r.Cost = 0 }},
		{name: "cost above limit", mutate: func(r *Request) { r.Cost = 2 }},
		{name: "short window", mutate: func(r *Request) { r.Window = MinWindow - time.Nanosecond }},
		{name: "long window", mutate: func(r *Request) { r.Window = MaxWindow + time.Nanosecond }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if _, err := limiter.Consume(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}

	if _, err := limiter.Consume(nil, valid); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil-context error = %v, want ErrInvalidRequest", err)
	}
	if _, err := limiter.CleanupExpired(context.Background(), 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("zero cleanup batch error = %v, want ErrInvalidRequest", err)
	}
	if _, err := limiter.CleanupExpired(context.Background(), MaxCleanupBatch+1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("oversized cleanup batch error = %v, want ErrInvalidRequest", err)
	}
}

func TestConsumeFailsClosedForCorruptState(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	limiter := newTestLimiter(t, db, clock)
	request := Request{
		Namespace: "login.ip",
		Key:       "203.0.113.42",
		Limit:     1,
		Cost:      1,
		Window:    time.Minute,
	}
	if _, err := limiter.Consume(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&windowRecord{}).
		Where("1 = 1").
		UpdateColumn("expires_at_ns", clock.Now().UnixNano()).
		Error; err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Consume(context.Background(), request); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("error = %v, want ErrInvalidState", err)
	}
}

func TestConstructorAndDatabaseErrors(t *testing.T) {
	if _, err := NewGORM(nil); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("nil database error = %v, want ErrDatabaseRequired", err)
	}
	db := openTestDatabase(t)
	if _, err := NewGORM(db, WithClock(nil)); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("nil clock error = %v, want ErrInvalidOption", err)
	}
	if _, err := NewGORM(db, WithMaxCASAttempts(0)); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("zero attempts error = %v, want ErrInvalidOption", err)
	}
	if _, err := NewGORM(db, nil); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("nil option error = %v, want ErrInvalidOption", err)
	}

	clock := newFakeClock(time.Date(2026, 7, 31, 8, 0, 0, 0, time.UTC))
	limiter := newTestLimiter(t, db, clock)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := limiter.Consume(cancelled, Request{
		Namespace: "login.ip",
		Key:       "cancelled",
		Limit:     1,
		Cost:      1,
		Window:    time.Minute,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled context error = %v, want context.Canceled", err)
	}
	if err := db.Exec("DROP TABLE " + TableName).Error; err != nil {
		t.Fatal(err)
	}
	_, err := limiter.Consume(context.Background(), Request{
		Namespace: "login.ip",
		Key:       "203.0.113.42",
		Limit:     1,
		Cost:      1,
		Window:    time.Minute,
	})
	if err == nil || errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("database error = %v, want propagated storage failure", err)
	}
}

func newTestLimiter(t *testing.T, db *gorm.DB, clock Clock) *GORMLimiter {
	t.Helper()
	limiter, err := NewGORM(db, WithClock(clock), WithMaxCASAttempts(512))
	if err != nil {
		t.Fatal(err)
	}
	return limiter
}

func openTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(t.TempDir()+"/rate-limit.db"),
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

func assertDecision(
	t *testing.T,
	decision Decision,
	allowed bool,
	remaining uint64,
	retryAfter time.Duration,
	resetAt time.Time,
) {
	t.Helper()
	if decision.Allowed != allowed ||
		decision.Remaining != remaining ||
		decision.RetryAfter != retryAfter ||
		!decision.ResetAt.Equal(resetAt) {
		t.Fatalf(
			"decision = %+v, want allowed=%v remaining=%d retryAfter=%s resetAt=%s",
			decision,
			allowed,
			remaining,
			retryAfter,
			resetAt,
		)
	}
}

type fakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (c *fakeClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

func (c *fakeClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}
