package idempotency

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestClaimCompleteAndReplay(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	store := newTestStore(t, db, clock)
	request := testClaimRequest()

	first, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if first.Disposition != DispositionExecute || first.Lease.Token == "" {
		t.Fatalf("first claim = %+v, want an execution lease", first)
	}
	var active gormRecord
	if err := db.First(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active.LeaseTokenHash == first.Lease.Token ||
		active.LeaseTokenHash != leaseTokenHash(first.Lease.Token) {
		t.Fatalf("execution lease was not hashed at rest: %+v", active)
	}

	pending, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Disposition != DispositionInProgress ||
		pending.RetryAfter != 20*time.Second ||
		!pending.LeaseExpiresAt.Equal(first.Lease.ExpiresAt) {
		t.Fatalf("pending claim = %+v, want a 20-second in-progress result", pending)
	}

	response := Response{
		Status:      http.StatusCreated,
		ContentType: "application/json; charset=utf-8",
		Headers: http.Header{
			"Location":      {"/api/v1/products/product-1"},
			"Cache-Control": {"private, max-age=0"},
			"ETag":          {`"version-1"`},
		},
		Body: []byte(`{"id":"product-1","name":"safe"}`),
	}
	completed, err := store.Complete(context.Background(), first.Lease, response)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Stored {
		t.Fatal("successful response was not stored")
	}

	replay, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Disposition != DispositionReplay || replay.Response == nil {
		t.Fatalf("replay claim = %+v, want stored response", replay)
	}
	assertResponse(t, *replay.Response, response)

	// Returned values are copies; a caller cannot mutate the stored replay.
	replay.Response.Body[0] = '!'
	replay.Response.Headers.Set("Location", "/mutated")
	again, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertResponse(t, *again.Response, response)

	var record gormRecord
	if err := db.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.KeyHash == request.Key || strings.Contains(record.ScopeHash, request.Key) {
		t.Fatalf("raw idempotency key leaked into persisted state: %+v", record)
	}
	if record.ActorID != request.Actor.ID || record.Route != request.Route {
		t.Fatalf("persisted scope = %+v, want actor and route", record)
	}
}

func TestClaimRejectsDigestConflict(t *testing.T) {
	store := newTestStore(
		t,
		openTestDatabase(t),
		newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)),
	)
	request := testClaimRequest()
	if _, err := store.Claim(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	request.RequestDigest = Digest([]byte(`{"name":"different"}`))
	if _, err := store.Claim(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting claim error = %v, want ErrConflict", err)
	}
}

func TestClaimScopesKeyByActorMethodAndRoute(t *testing.T) {
	store := newTestStore(
		t,
		openTestDatabase(t),
		newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)),
	)
	base := testClaimRequest()
	requests := []ClaimRequest{
		base,
		func() ClaimRequest {
			request := base
			request.Actor.ID = "user-2"
			return request
		}(),
		func() ClaimRequest {
			request := base
			request.Method = http.MethodPatch
			return request
		}(),
		func() ClaimRequest {
			request := base
			request.Route = "/api/v1/files"
			return request
		}(),
	}
	for _, request := range requests {
		result, err := store.Claim(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if result.Disposition != DispositionExecute {
			t.Fatalf("independent scope claim = %+v, want execute", result)
		}
	}
}

func TestConcurrentClaimsGrantExactlyOneExecutionLease(t *testing.T) {
	const callers = 96
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// SQLite serializes the individual statements while the test still
	// interleaves the read/insert/read claim protocol across store instances.
	sqlDB.SetMaxOpenConns(1)
	firstStore := newTestStore(t, db, clock)
	secondStore := newTestStore(t, db, clock)
	request := testClaimRequest()
	start := make(chan struct{})
	results := make(chan ClaimResult, callers)
	errs := make(chan error, callers)
	var group sync.WaitGroup
	for index := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			current := firstStore
			if index%2 == 0 {
				current = secondStore
			}
			result, claimErr := current.Claim(context.Background(), request)
			if claimErr != nil {
				errs <- claimErr
				return
			}
			results <- result
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)

	for claimErr := range errs {
		t.Errorf("concurrent claim: %v", claimErr)
	}
	executions := 0
	inProgress := 0
	for result := range results {
		switch result.Disposition {
		case DispositionExecute:
			executions++
		case DispositionInProgress:
			inProgress++
		default:
			t.Errorf("unexpected concurrent disposition %q", result.Disposition)
		}
	}
	if executions != 1 || inProgress != callers-1 {
		t.Fatalf(
			"execute=%d in-progress=%d, want 1 and %d",
			executions,
			inProgress,
			callers-1,
		)
	}
}

func TestExpiredLeaseCanBeRecoveredAndOldOwnerCannotComplete(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	store := newTestStore(t, openTestDatabase(t), clock)
	request := testClaimRequest()
	first, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	clock.Advance(21 * time.Second)
	recovered, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Disposition != DispositionExecute ||
		recovered.Lease.Token == first.Lease.Token {
		t.Fatalf("recovered claim = %+v, want a fresh execution lease", recovered)
	}

	response := testResponse(http.StatusCreated)
	if _, err := store.Complete(
		context.Background(),
		first.Lease,
		response,
	); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("old lease completion error = %v, want ErrLeaseLost", err)
	}
	if _, err := store.Complete(context.Background(), recovered.Lease, response); err != nil {
		t.Fatal(err)
	}
}

func TestRenewExtendsLeaseAndUsesBoundedOwnershipToken(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	store := newTestStore(t, openTestDatabase(t), clock)
	request := testClaimRequest()
	first, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	clock.Advance(15 * time.Second)
	renewed, err := store.Renew(context.Background(), first.Lease)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Token != first.Lease.Token ||
		!renewed.ExpiresAt.Equal(clock.Now().Add(20*time.Second)) {
		t.Fatalf("renewed lease = %+v, want same token and extended expiry", renewed)
	}
	clock.Advance(10 * time.Second)
	pending, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Disposition != DispositionInProgress || pending.RetryAfter != 10*time.Second {
		t.Fatalf("claim after renewal = %+v, want in-progress", pending)
	}
}

func TestExpiredLeaseCannotBeRenewedOrCompleted(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	store := newTestStore(t, openTestDatabase(t), clock)
	claimed, err := store.Claim(context.Background(), testClaimRequest())
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(20 * time.Second)
	if _, err := store.Renew(
		context.Background(),
		claimed.Lease,
	); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired renewal error = %v, want ErrLeaseLost", err)
	}
	if _, err := store.Complete(
		context.Background(),
		claimed.Lease,
		testResponse(http.StatusCreated),
	); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired completion error = %v, want ErrLeaseLost", err)
	}
}

func TestAbandonMakesRequestImmediatelyExecutable(t *testing.T) {
	store := newTestStore(
		t,
		openTestDatabase(t),
		newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)),
	)
	request := testClaimRequest()
	first, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Abandon(context.Background(), first.Lease); err != nil {
		t.Fatal(err)
	}
	second, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disposition != DispositionExecute || second.Lease.Token == first.Lease.Token {
		t.Fatalf("claim after abandon = %+v, want fresh execution", second)
	}
	if err := store.Abandon(
		context.Background(),
		first.Lease,
	); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale abandon error = %v, want ErrLeaseLost", err)
	}
}

func TestServerErrorIsNotCachedByDefault(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	store := newTestStore(t, openTestDatabase(t), clock)
	request := testClaimRequest()
	first, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.Complete(
		context.Background(),
		first.Lease,
		Response{
			Status:      http.StatusInternalServerError,
			ContentType: "application/json",
			Headers:     http.Header{"Set-Cookie": {"must-not-be-inspected"}},
			Body:        []byte(`{"refresh_token":"must-not-be-inspected"}`),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Stored {
		t.Fatal("5xx response was cached by default")
	}

	second, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Disposition != DispositionExecute || second.Lease.Token == first.Lease.Token {
		t.Fatalf("claim after 5xx = %+v, want a new execution lease", second)
	}
}

func TestServerErrorCanBeCachedExplicitly(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	store, err := NewGORM(
		db,
		WithClock(clock),
		WithLeaseDuration(20*time.Second),
		WithTTL(time.Hour),
		WithCacheServerErrors(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := testClaimRequest()
	first, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.Complete(
		context.Background(),
		first.Lease,
		testResponse(http.StatusBadGateway),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.Stored {
		t.Fatal("explicitly enabled 5xx response was not stored")
	}
	replay, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Disposition != DispositionReplay ||
		replay.Response.Status != http.StatusBadGateway {
		t.Fatalf("5xx replay = %+v", replay)
	}
}

func TestResponseValidationRejectsOversizedOrSensitiveData(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	store := newTestStore(t, openTestDatabase(t), clock)
	tests := []struct {
		name     string
		response Response
		want     error
	}{
		{
			name: "oversized body",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Body:        bytes.Repeat([]byte("x"), MaxResponseBodyBytes+1),
			},
			want: ErrResponseTooLarge,
		},
		{
			name: "secret header",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Headers:     http.Header{"Set-Cookie": {"session=secret"}},
				Body:        []byte(`{"id":"safe"}`),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "unapproved custom header",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Headers:     http.Header{"X-Internal-Token": {"secret"}},
				Body:        []byte(`{"id":"safe"}`),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "signed location header",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Headers: http.Header{
					"Location": {"https://objects.example/file?X-Amz-Signature=secret"},
				},
				Body: []byte(`{"id":"safe"}`),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "oversized headers",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Headers: http.Header{
					"Location": {"/" + strings.Repeat("x", MaxResponseHeadersBytes)},
				},
				Body: []byte(`{"id":"safe"}`),
			},
			want: ErrResponseTooLarge,
		},
		{
			name: "secret JSON field",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Body:        []byte(`{"account":{"refresh_token":"secret"}}`),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "derived secret JSON field",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Body:        []byte(`{"account":{"authToken":"secret"}}`),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "signed URL JSON value",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Body: []byte(
					`{"url":"https://objects.example/file?X-Amz-Signature=secret"}`,
				),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "invalid JSON",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Body:        []byte(`{"id":`),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "invalid trailing JSON",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json",
				Body:        []byte(`{"id":"safe"} trailing`),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "non JSON body",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "text/plain",
				Body:        []byte("opaque secrets cannot be inspected"),
			},
			want: ErrUnsafeResponse,
		},
		{
			name: "secret content type parameter",
			response: Response{
				Status:      http.StatusCreated,
				ContentType: "application/json; access_token=secret",
				Body:        []byte(`{"id":"safe"}`),
			},
			want: ErrUnsafeResponse,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := testClaimRequest()
			request.Key = "key-" + strings.ReplaceAll(test.name, " ", "-")
			claimed, err := store.Claim(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Complete(
				context.Background(),
				claimed.Lease,
				test.response,
			); !errors.Is(err, test.want) {
				t.Fatalf("response error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCompletionCanCommitWithBusinessTransaction(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	store := newTestStore(t, db, clock)
	request := testClaimRequest()
	claimed, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	type businessRow struct {
		ID   int `gorm:"primaryKey"`
		Name string
	}
	if err := db.Exec("CREATE TABLE business_rows (id INTEGER PRIMARY KEY, name TEXT NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	rollbackErr := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&businessRow{ID: 1, Name: "rolled back"}).Error; err != nil {
			return err
		}
		transactionStore, err := store.WithDB(tx)
		if err != nil {
			return err
		}
		if _, err := transactionStore.Complete(
			context.Background(),
			claimed.Lease,
			testResponse(http.StatusCreated),
		); err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	if rollbackErr == nil {
		t.Fatal("transaction unexpectedly committed")
	}
	var count int64
	if err := db.Table("business_rows").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("business rows after rollback = %d, want 0", count)
	}
	stillPending, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if stillPending.Disposition != DispositionInProgress {
		t.Fatalf("claim after rollback = %+v, want in-progress", stillPending)
	}

	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&businessRow{ID: 2, Name: "committed"}).Error; err != nil {
			return err
		}
		transactionStore, err := store.WithDB(tx)
		if err != nil {
			return err
		}
		_, err = transactionStore.Complete(
			context.Background(),
			claimed.Lease,
			testResponse(http.StatusCreated),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	replay, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Disposition != DispositionReplay {
		t.Fatalf("claim after commit = %+v, want replay", replay)
	}
}

func TestExpiredRecordCanBeReusedByDifferentDigest(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	store, err := NewGORM(
		db,
		WithClock(clock),
		WithLeaseDuration(time.Second),
		WithTTL(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := testClaimRequest()
	first, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Complete(
		context.Background(),
		first.Lease,
		testResponse(http.StatusCreated),
	); err != nil {
		t.Fatal(err)
	}

	clock.Advance(time.Minute + time.Nanosecond)
	request.RequestDigest = Digest([]byte(`{"name":"reused"}`))
	reused, err := store.Claim(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Disposition != DispositionExecute || reused.Lease.Token == first.Lease.Token {
		t.Fatalf("reused expired record = %+v, want new execution lease", reused)
	}
}

func TestCleanupExpiredIsBounded(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	store, err := NewGORM(
		db,
		WithClock(clock),
		WithLeaseDuration(time.Second),
		WithTTL(time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"expired-1", "expired-2", "expired-3"} {
		request := testClaimRequest()
		request.Key = key
		claim, err := store.Claim(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Complete(
			context.Background(),
			claim.Lease,
			testResponse(http.StatusCreated),
		); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Minute + time.Nanosecond)
	activeRequest := testClaimRequest()
	activeRequest.Key = "active"
	if _, err := store.Claim(context.Background(), activeRequest); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.CleanupExpired(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("first cleanup deleted %d rows, want 2", deleted)
	}
	deleted, err = store.CleanupExpired(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("second cleanup deleted %d rows, want 1", deleted)
	}
	var count int64
	if err := db.Model(&gormRecord{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("records after cleanup = %d, want 1", count)
	}
}

func TestInvalidInputsFailBeforeDatabaseAccess(t *testing.T) {
	clock := newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC))
	db := openTestDatabase(t)
	store := newTestStore(t, db, clock)
	if err := db.Exec("DROP TABLE " + TableName).Error; err != nil {
		t.Fatal(err)
	}
	valid := testClaimRequest()
	tests := []struct {
		name   string
		mutate func(*ClaimRequest)
	}{
		{name: "actor kind", mutate: func(r *ClaimRequest) { r.Actor.Kind = "admin" }},
		{name: "actor ID", mutate: func(r *ClaimRequest) { r.Actor.ID = "" }},
		{name: "long actor ID", mutate: func(r *ClaimRequest) { r.Actor.ID = strings.Repeat("a", MaxActorIDBytes+1) }},
		{name: "method", mutate: func(r *ClaimRequest) { r.Method = "P OST" }},
		{name: "relative route", mutate: func(r *ClaimRequest) { r.Route = "api/v1/products" }},
		{name: "query in route", mutate: func(r *ClaimRequest) { r.Route += "?unsafe=1" }},
		{name: "long route", mutate: func(r *ClaimRequest) { r.Route = "/" + strings.Repeat("a", MaxRouteBytes) }},
		{name: "empty key", mutate: func(r *ClaimRequest) { r.Key = "" }},
		{name: "long key", mutate: func(r *ClaimRequest) { r.Key = strings.Repeat("a", MaxKeyBytes+1) }},
		{name: "space in key", mutate: func(r *ClaimRequest) { r.Key = "not valid" }},
		{name: "uppercase digest", mutate: func(r *ClaimRequest) { r.RequestDigest = strings.ToUpper(r.RequestDigest) }},
		{name: "short digest", mutate: func(r *ClaimRequest) { r.RequestDigest = "abcd" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.mutate(&request)
			if _, err := store.Claim(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want ErrInvalidRequest", err)
			}
		})
	}
	if _, err := store.Claim(nil, valid); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("nil context error = %v, want ErrInvalidRequest", err)
	}
	if _, err := store.CleanupExpired(context.Background(), 0); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("zero cleanup error = %v, want ErrInvalidRequest", err)
	}
	if _, err := store.Claim(context.Background(), valid); err == nil ||
		errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("valid request database error = %v, want propagated storage failure", err)
	}
}

func TestCorruptStateFailsClosed(t *testing.T) {
	store := newTestStore(
		t,
		openTestDatabase(t),
		newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)),
	)
	request := testClaimRequest()
	if _, err := store.Claim(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&gormRecord{}).
		Where("1 = 1").
		UpdateColumn("updated_at_ns", int64(1)).
		Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(
		context.Background(),
		request,
	); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("corrupt-state error = %v, want ErrInvalidState", err)
	}
}

func TestConstructorAndContextValidation(t *testing.T) {
	if _, err := NewGORM(nil); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("nil database error = %v, want ErrDatabaseRequired", err)
	}
	db := openTestDatabase(t)
	tests := []struct {
		name   string
		option GORMOption
	}{
		{name: "nil clock", option: WithClock(nil)},
		{name: "short lease", option: WithLeaseDuration(MinLeaseDuration - time.Nanosecond)},
		{name: "long lease", option: WithLeaseDuration(MaxLeaseDuration + time.Nanosecond)},
		{name: "short TTL", option: WithTTL(MinTTL - time.Nanosecond)},
		{name: "long TTL", option: WithTTL(MaxTTL + time.Nanosecond)},
		{name: "zero CAS", option: WithMaxCASAttempts(0)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewGORM(db, test.option); !errors.Is(err, ErrInvalidOption) {
				t.Fatalf("constructor error = %v, want ErrInvalidOption", err)
			}
		})
	}
	if _, err := NewGORM(db, nil); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("nil option error = %v, want ErrInvalidOption", err)
	}
	if _, err := NewGORM(
		db,
		WithLeaseDuration(2*time.Minute),
		WithTTL(time.Minute),
	); !errors.Is(err, ErrInvalidOption) {
		t.Fatalf("TTL shorter than lease error = %v, want ErrInvalidOption", err)
	}
	store := newTestStore(
		t,
		db,
		newFakeClock(time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)),
	)
	if _, err := store.WithDB(nil); !errors.Is(err, ErrDatabaseRequired) {
		t.Fatalf("nil bound database error = %v, want ErrDatabaseRequired", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Claim(
		cancelled,
		testClaimRequest(),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled claim error = %v, want context.Canceled", err)
	}
}

func testClaimRequest() ClaimRequest {
	return ClaimRequest{
		Actor:         Actor{Kind: ActorKindUser, ID: "user-1"},
		Method:        http.MethodPost,
		Route:         "/api/v1/products",
		Key:           "018f86c7-a1a2-7f00-9000-123456789abc",
		RequestDigest: Digest([]byte(`{"name":"Aginex"}`)),
	}
}

func testResponse(status int) Response {
	return Response{
		Status:      status,
		ContentType: "application/json",
		Body:        []byte(`{"id":"product-1"}`),
	}
}

func assertResponse(t *testing.T, actual, expected Response) {
	t.Helper()
	if actual.Status != expected.Status ||
		actual.ContentType != expected.ContentType ||
		!bytes.Equal(actual.Body, expected.Body) {
		t.Fatalf("response = %+v, want %+v", actual, expected)
	}
	if len(actual.Headers) != len(expected.Headers) {
		t.Fatalf("headers = %#v, want %#v", actual.Headers, expected.Headers)
	}
	for name, values := range expected.Headers {
		actualValues := actual.Headers.Values(name)
		if strings.Join(actualValues, "\x00") != strings.Join(values, "\x00") {
			t.Fatalf("header %s = %#v, want %#v", name, actualValues, values)
		}
	}
}

func newTestStore(t *testing.T, db *gorm.DB, clock Clock) *GORMStore {
	t.Helper()
	store, err := NewGORM(
		db,
		WithClock(clock),
		WithLeaseDuration(20*time.Second),
		WithTTL(time.Hour),
		WithMaxCASAttempts(512),
	)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func openTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(t.TempDir()+"/idempotency.db"),
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

type fakeClock struct {
	mu  sync.RWMutex
	now time.Time
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
}
