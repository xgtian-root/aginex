package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/jobs"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPostgresQueueDeliveryAndRecovery(t *testing.T) {
	dsn := os.Getenv("AGINEX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGINEX_TEST_POSTGRES_DSN is not configured")
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := sqlDB.Ping(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := Up(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if err := EnsureCurrent(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, "TRUNCATE TABLE aginex_jobs"); err != nil {
		t.Fatal(err)
	}

	gormDB, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: sqlDB}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	queue, err := New(gormDB, Config{
		LeaseDuration: time.Minute,
		BaseRetry:     time.Second,
		MaxRetry:      time.Minute,
		Clock:         func() time.Time { return now },
		Jitter:        func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := jobs.EnqueueRequest{
		Type:           "storage.cleanup",
		Version:        1,
		Payload:        json.RawMessage(`{"objectKey":"uploads/a.png"}`),
		IdempotencyKey: "file-1:delete",
		MaxAttempts:    3,
		CreatedBy:      authz.NewUserActor("user-1"),
		Trace:          jobs.TraceContext{RequestID: "request-1"},
	}
	enqueued, err := queue.Enqueue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !enqueued.Created {
		t.Fatal("first enqueue was not created")
	}
	duplicate, err := queue.Enqueue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate.Created || duplicate.Job.ID != enqueued.Job.ID {
		t.Fatalf("duplicate enqueue = %#v", duplicate)
	}
	conflicting := request
	conflicting.Payload = json.RawMessage(`{"objectKey":"uploads/b.png"}`)
	if _, err := queue.Enqueue(ctx, conflicting); !errors.Is(err, jobs.ErrIdempotencyConflict) {
		t.Fatalf("conflicting enqueue error = %v", err)
	}

	firstClaim, err := queue.Claim(ctx, jobs.ClaimRequest{WorkerID: "worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(firstClaim) != 1 || firstClaim[0].Attempts != 1 {
		t.Fatalf("first claim = %#v", firstClaim)
	}
	secondClaim, err := queue.Claim(ctx, jobs.ClaimRequest{WorkerID: "worker-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(secondClaim) != 0 {
		t.Fatalf("concurrent claim returned %#v", secondClaim)
	}
	opaqueFailure := errors.New(
		"temporary object-store failure\r\n" +
			"password=hunter2 signed-url=secret\x00",
	)
	state, err := queue.Fail(ctx, enqueued.Job.ID, "worker-1", opaqueFailure)
	if err != nil {
		t.Fatal(err)
	}
	if state != jobs.StateFailed {
		t.Fatalf("failed state = %q", state)
	}
	var storedError string
	if err := sqlDB.QueryRowContext(
		ctx,
		"SELECT last_error FROM aginex_jobs WHERE id = $1",
		enqueued.Job.ID,
	).Scan(&storedError); err != nil {
		t.Fatal(err)
	}
	if storedError != storedFailureSummary {
		t.Fatalf("stored last_error = %q", storedError)
	}

	now = now.Add(2 * time.Second)
	retryClaim, err := queue.Claim(ctx, jobs.ClaimRequest{WorkerID: "worker-2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(retryClaim) != 1 || retryClaim[0].Attempts != 2 {
		t.Fatalf("retry claim = %#v", retryClaim)
	}
	if err := queue.Succeed(ctx, enqueued.Job.ID, "worker-2"); err != nil {
		t.Fatal(err)
	}
	if err := queue.Heartbeat(ctx, enqueued.Job.ID, "worker-2"); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("heartbeat after completion error = %v", err)
	}
}

func TestPostgresQueueTransactionAndStaleLeaseRecovery(t *testing.T) {
	dsn := os.Getenv("AGINEX_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGINEX_TEST_POSTGRES_DSN is not configured")
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	if err := Up(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.ExecContext(ctx, "TRUNCATE TABLE aginex_jobs"); err != nil {
		t.Fatal(err)
	}
	gormDB, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: sqlDB}))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	queue, err := New(gormDB, Config{
		LeaseDuration: time.Minute,
		Clock:         func() time.Time { return now },
		Jitter:        func() float64 { return 0 },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := jobs.EnqueueRequest{
		Type:           "notifications.send",
		Version:        1,
		Payload:        json.RawMessage(`{"messageId":"message-1"}`),
		IdempotencyKey: "message-1",
		MaxAttempts:    2,
		CreatedBy:      authz.NewSystemActor("api"),
	}
	rollback := errors.New("rollback")
	err = gormDB.Transaction(func(tx *gorm.DB) error {
		transactionalQueue, err := queue.WithDB(tx)
		if err != nil {
			return err
		}
		if _, err := transactionalQueue.Enqueue(ctx, request); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction error = %v", err)
	}
	var count int64
	if err := gormDB.Table("aginex_jobs").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("jobs after rollback = %d", count)
	}

	enqueued, err := queue.Enqueue(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Claim(ctx, jobs.ClaimRequest{WorkerID: "worker-a"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	reclaimed, err := queue.Claim(ctx, jobs.ClaimRequest{WorkerID: "worker-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 1 || reclaimed[0].ID != enqueued.Job.ID || reclaimed[0].Attempts != 2 {
		t.Fatalf("reclaimed jobs = %#v", reclaimed)
	}
	state, err := queue.Fail(ctx, enqueued.Job.ID, "worker-b", errors.New("permanent failure"))
	if err != nil {
		t.Fatal(err)
	}
	if state != jobs.StateDead {
		t.Fatalf("terminal state = %q", state)
	}
	dead, err := queue.List(ctx, jobs.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if dead.Total != 1 ||
		len(dead.Jobs) != 1 ||
		dead.Jobs[0].ID != enqueued.Job.ID ||
		dead.Jobs[0].State != jobs.StateDead {
		t.Fatalf("dead jobs = %#v", dead)
	}
	filtered, err := queue.List(ctx, jobs.ListRequest{
		State: jobs.StateDead,
		Type:  "storage.cleanup",
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 0 || len(filtered.Jobs) != 0 {
		t.Fatalf("filtered jobs = %#v, want empty", filtered)
	}
	if err := queue.RetryDead(ctx, enqueued.Job.ID); err != nil {
		t.Fatal(err)
	}
	if err := queue.RetryDead(ctx, enqueued.Job.ID); !errors.Is(err, jobs.ErrInvalidTransition) {
		t.Fatalf("retry pending job error = %v, want ErrInvalidTransition", err)
	}
	if err := queue.RetryDead(
		ctx,
		"11111111-1111-4111-8111-111111111111",
	); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("retry missing job error = %v, want ErrNotFound", err)
	}
	retried, err := queue.Claim(ctx, jobs.ClaimRequest{WorkerID: "worker-c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(retried) != 1 || retried[0].Attempts != 1 {
		t.Fatalf("manual retry claim = %#v", retried)
	}
}
