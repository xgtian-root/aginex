package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	frameworkjobs "github.com/xgtian-root/aginex/server/framework/jobs"
	"github.com/xgtian-root/aginex/server/framework/module"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"gorm.io/gorm"
)

const (
	deadJobID    = "e4f2cc53-08aa-46ae-8b63-80a8df8d5b69"
	pendingJobID = "a1b2c3d4-1234-4abc-8def-1234567890ab"
)

func TestJobAdministrationIsProtectedFilteredSafeAndAudited(t *testing.T) {
	server, db, queue, adminCookie := newJobAdminTestApp(t)

	unauthenticated := serveRequest(
		server,
		nil,
		http.MethodGet,
		"/api/v1/jobs",
		nil,
		"",
	)
	assertProblemCode(
		t,
		unauthenticated,
		http.StatusUnauthorized,
		"AUTHENTICATION_REQUIRED",
	)

	const (
		viewerEmail    = "job-viewer@example.com"
		viewerPassword = "job viewer password value"
	)
	createFileUser(t, db, viewerEmail, viewerPassword, frameworkauthz.ScopeOwn)
	viewerCookie := loginCookieAs(t, server, viewerEmail, viewerPassword)
	forbidden := serveRequest(
		server,
		viewerCookie,
		http.MethodGet,
		"/api/v1/jobs",
		nil,
		"",
	)
	assertProblemCode(t, forbidden, http.StatusForbidden, "RESOURCE_FORBIDDEN")

	list := serveRequest(
		server,
		adminCookie,
		http.MethodGet,
		"/api/v1/jobs?type=storage.cleanup&page=1&pageSize=10",
		nil,
		"",
	)
	if list.Code != http.StatusOK {
		t.Fatalf("list jobs status = %d, body = %s", list.Code, list.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
		Page  int              `json:"page"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Page != 1 || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("job page = %#v", page)
	}
	item := page.Items[0]
	if item["id"] != deadJobID || item["state"] != string(frameworkjobs.StateDead) {
		t.Fatalf("dead job response = %#v", item)
	}
	if item["hasError"] != true {
		t.Fatalf("dead job must indicate a stored error without exposing it: %#v", item)
	}
	for _, privateField := range []string{
		"payload",
		"payloadSHA256",
		"idempotencyKey",
		"traceparent",
		"lastError",
	} {
		if _, exposed := item[privateField]; exposed {
			t.Fatalf("job response exposes %q: %#v", privateField, item)
		}
	}
	if request := queue.lastListRequest(); request.State != frameworkjobs.StateDead ||
		request.Type != "storage.cleanup" ||
		request.Page != 1 ||
		request.PageSize != 10 {
		t.Fatalf("list request = %#v", request)
	}

	pending := serveRequest(
		server,
		adminCookie,
		http.MethodGet,
		"/api/v1/jobs?state=pending&type=storage.cleanup",
		nil,
		"",
	)
	if pending.Code != http.StatusOK {
		t.Fatalf("list pending status = %d, body = %s", pending.Code, pending.Body.String())
	}
	if err := json.Unmarshal(pending.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0]["id"] != pendingJobID {
		t.Fatalf("pending jobs = %#v", page)
	}

	invalidFilter := serveRequest(
		server,
		adminCookie,
		http.MethodGet,
		"/api/v1/jobs?state=lost",
		nil,
		"",
	)
	assertProblemCode(t, invalidFilter, http.StatusBadRequest, "REQUEST_INVALID")

	retried := serveRequest(
		server,
		adminCookie,
		http.MethodPost,
		"/api/v1/jobs/"+deadJobID+"/retry",
		nil,
		"",
	)
	if retried.Code != http.StatusAccepted {
		t.Fatalf("retry dead job status = %d, body = %s", retried.Code, retried.Body.String())
	}
	var retryResponse JobRetryResponse
	if err := json.Unmarshal(retried.Body.Bytes(), &retryResponse); err != nil {
		t.Fatal(err)
	}
	if retryResponse.ID != deadJobID ||
		retryResponse.State != string(frameworkjobs.StatePending) {
		t.Fatalf("retry response = %#v", retryResponse)
	}
	assertJobAdminState(t, db, deadJobID, frameworkjobs.StatePending)

	var event domain.AuditLog
	if err := db.Where(
		"action = ? AND resource_id = ?",
		"jobs:retry",
		deadJobID,
	).First(&event).Error; err != nil {
		t.Fatal(err)
	}
	if event.ActorID == nil ||
		event.Resource != "job" ||
		event.Result != "success" ||
		event.Source != "http" ||
		event.Before["state"] != string(frameworkjobs.StateDead) ||
		event.After["state"] != string(frameworkjobs.StatePending) {
		t.Fatalf("job retry audit event = %#v", event)
	}

	conflict := serveRequest(
		server,
		adminCookie,
		http.MethodPost,
		"/api/v1/jobs/"+deadJobID+"/retry",
		nil,
		"",
	)
	assertProblemCode(t, conflict, http.StatusConflict, "REQUEST_CONFLICT")

	missing := serveRequest(
		server,
		adminCookie,
		http.MethodPost,
		"/api/v1/jobs/11111111-1111-4111-8111-111111111111/retry",
		nil,
		"",
	)
	assertProblemCode(t, missing, http.StatusNotFound, "RESOURCE_NOT_FOUND")

	invalidID := serveRequest(
		server,
		adminCookie,
		http.MethodPost,
		"/api/v1/jobs/not-a-uuid/retry",
		nil,
		"",
	)
	assertProblemCode(t, invalidID, http.StatusBadRequest, "REQUEST_INVALID")
}

func TestJobRetryRollsBackWhenAuditInsertFails(t *testing.T) {
	server, db, _, adminCookie := newJobAdminTestApp(t)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	response := serveRequest(
		server,
		adminCookie,
		http.MethodPost,
		"/api/v1/jobs/"+deadJobID+"/retry",
		nil,
		"",
	)
	assertProblemCode(t, response, http.StatusInternalServerError, "INTERNAL_ERROR")
	assertJobAdminState(t, db, deadJobID, frameworkjobs.StateDead)
}

func TestJobRetryIsIdempotentWhenAKeyIsProvided(t *testing.T) {
	server, db, _, adminCookie := newJobAdminTestApp(t)
	const key = "retry-dead-job-once"

	first := serveJobRetryWithKey(server, adminCookie, deadJobID, key)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first retry status = %d, body = %s", first.Code, first.Body.String())
	}
	second := serveJobRetryWithKey(server, adminCookie, deadJobID, key)
	if second.Code != http.StatusAccepted {
		t.Fatalf("replayed retry status = %d, body = %s", second.Code, second.Body.String())
	}
	if second.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf("replayed header = %q", second.Header().Get(idempotencyReplayedHeader))
	}
	if second.Body.String() != first.Body.String() {
		t.Fatalf(
			"replayed body = %q, want %q",
			second.Body.String(),
			first.Body.String(),
		)
	}
	assertJobAdminState(t, db, deadJobID, frameworkjobs.StatePending)
	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "jobs:retry", deadJobID).
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("job retry audit count = %d, want 1", auditCount)
	}
}

func TestJobAdministrationReturnsUnavailableWithoutDurableQueue(t *testing.T) {
	server, _, _, adminCookie := newJobAdminTestApp(t)
	server.jobs = nil
	server.jobInspector = nil

	list := serveRequest(
		server,
		adminCookie,
		http.MethodGet,
		"/api/v1/jobs",
		nil,
		"",
	)
	assertProblemCode(t, list, http.StatusServiceUnavailable, "JOBS_UNAVAILABLE")

	retry := serveRequest(
		server,
		adminCookie,
		http.MethodPost,
		"/api/v1/jobs/"+deadJobID+"/retry",
		nil,
		"",
	)
	assertProblemCode(t, retry, http.StatusServiceUnavailable, "JOBS_UNAVAILABLE")
}

func TestOpenAPIJobAdministrationContract(t *testing.T) {
	document := BuildOpenAPI()
	list := operationAt(document, module.MethodGet, "/api/v1/jobs")
	if list == nil || list.OperationID != "listJobs" {
		t.Fatal("GET /api/v1/jobs is missing")
	}
	for _, status := range []string{"200", "400", "401", "403", "503", "500", "default"} {
		if list.Responses[status] == nil {
			t.Fatalf("list jobs is missing response %s", status)
		}
	}
	stateFound := false
	typeFound := false
	for _, parameter := range list.Parameters {
		switch parameter.Name {
		case "state":
			stateFound = parameter.Schema != nil &&
				parameter.Schema.Default == string(frameworkjobs.StateDead)
		case "type":
			typeFound = parameter.Schema != nil &&
				parameter.Schema.MaxLength != nil &&
				*parameter.Schema.MaxLength == 120
		}
	}
	if !stateFound || !typeFound {
		t.Fatalf("list jobs filters = %#v", list.Parameters)
	}

	retry := operationAt(
		document,
		module.MethodPost,
		"/api/v1/jobs/{id}/retry",
	)
	if retry == nil || retry.OperationID != "retryDeadJob" || retry.RequestBody != nil {
		t.Fatalf("retry dead job operation = %#v", retry)
	}
	for _, status := range []string{"202", "400", "401", "403", "404", "409", "503", "500", "default"} {
		if retry.Responses[status] == nil {
			t.Fatalf("retry dead job is missing response %s", status)
		}
	}
	if len(retry.Security) != 1 {
		t.Fatalf("retry dead job security = %#v", retry.Security)
	}
	if _, ok := retry.Security[0][sessionScheme]; !ok {
		t.Fatalf("retry dead job security = %#v", retry.Security)
	}
	idempotencyFound := false
	for _, parameter := range retry.Parameters {
		if parameter.Name == idempotencyHeader && parameter.In == "header" {
			idempotencyFound = parameter.Schema != nil &&
				parameter.Schema.MaxLength != nil &&
				*parameter.Schema.MaxLength == 200
		}
	}
	if !idempotencyFound {
		t.Fatalf("retry dead job idempotency contract = %#v", retry.Parameters)
	}

	schema := document.Components.Schemas.Map()["JobResponse"]
	if schema == nil {
		t.Fatal("JobResponse schema is missing")
	}
	for _, privateField := range []string{
		"payload",
		"payloadSHA256",
		"idempotencyKey",
		"traceparent",
		"lastError",
	} {
		if _, exposed := schema.Properties[privateField]; exposed {
			t.Fatalf("JobResponse schema exposes %q", privateField)
		}
	}
}

func serveJobRetryWithKey(
	server *App,
	cookie *http.Cookie,
	id string,
	key string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/jobs/"+id+"/retry",
		nil,
	)
	request.AddCookie(cookie)
	request.Header.Set(idempotencyHeader, key)
	addTestCSRF(request)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func newJobAdminTestApp(
	t *testing.T,
) (*App, *gorm.DB, *sqliteJobAdminQueue, *http.Cookie) {
	t.Helper()
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "correct horse battery staple",
		},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
		WebOrigin: "http://localhost:3000",
	}
	db := openMigratedDatabase(t, cfg.Database)
	bootstrapTestData(t, db, cfg.Bootstrap)
	if err := db.Exec(`
		CREATE TABLE job_admin_test_states (
			id TEXT PRIMARY KEY,
			state TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	for id, state := range map[string]frameworkjobs.State{
		deadJobID:    frameworkjobs.StateDead,
		pendingJobID: frameworkjobs.StatePending,
	} {
		if err := db.Exec(
			"INSERT INTO job_admin_test_states (id, state) VALUES (?, ?)",
			id,
			state,
		).Error; err != nil {
			t.Fatal(err)
		}
	}
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	queue := &sqliteJobAdminQueue{
		db: db,
		state: &jobAdminQueueState{jobs: []frameworkjobs.Job{
			{
				ID:             deadJobID,
				Type:           "storage.cleanup",
				Version:        2,
				Payload:        json.RawMessage(`{"secret":"must not leak"}`),
				PayloadSHA256:  "must-not-leak",
				IdempotencyKey: "must-not-leak",
				State:          frameworkjobs.StateDead,
				ScheduledAt:    now,
				Attempts:       3,
				MaxAttempts:    3,
				LastError:      "signed URL and payload must not leak",
				CreatedBy:      frameworkauthz.NewSystemActor("api"),
				Trace: frameworkjobs.TraceContext{
					RequestID:   "request-dead",
					TraceParent: "must-not-leak",
				},
				CreatedAt:   now.Add(-time.Hour),
				UpdatedAt:   now,
				CompletedAt: &now,
			},
			{
				ID:          pendingJobID,
				Type:        "storage.cleanup",
				Version:     2,
				State:       frameworkjobs.StatePending,
				ScheduledAt: now.Add(time.Minute),
				MaxAttempts: 3,
				CreatedBy:   frameworkauthz.NewUserActor("user-1"),
				CreatedAt:   now,
				UpdatedAt:   now,
			},
		}},
	}
	server.jobs = queue
	server.jobInspector = queue
	return server, db, queue, loginCookie(t, server)
}

func assertJobAdminState(
	t *testing.T,
	db *gorm.DB,
	id string,
	want frameworkjobs.State,
) {
	t.Helper()
	var state frameworkjobs.State
	if err := db.Raw(
		"SELECT state FROM job_admin_test_states WHERE id = ?",
		id,
	).Row().Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != want {
		t.Fatalf("job %s state = %q, want %q", id, state, want)
	}
}

type jobAdminQueueState struct {
	mu       sync.Mutex
	jobs     []frameworkjobs.Job
	lastList frameworkjobs.ListRequest
}

type sqliteJobAdminQueue struct {
	db    *gorm.DB
	state *jobAdminQueueState
}

func (queue *sqliteJobAdminQueue) Bind(db *gorm.DB) (frameworkjobs.Queue, error) {
	if db == nil {
		return nil, frameworkjobs.ErrInvalid
	}
	return &sqliteJobAdminQueue{db: db, state: queue.state}, nil
}

func (queue *sqliteJobAdminQueue) List(
	_ context.Context,
	request frameworkjobs.ListRequest,
) (frameworkjobs.ListResult, error) {
	normalized, err := frameworkjobs.NormalizeList(request)
	if err != nil {
		return frameworkjobs.ListResult{}, err
	}
	queue.state.mu.Lock()
	defer queue.state.mu.Unlock()
	queue.state.lastList = normalized
	filtered := make([]frameworkjobs.Job, 0, len(queue.state.jobs))
	for _, job := range queue.state.jobs {
		if job.State != normalized.State {
			continue
		}
		if normalized.Type != "" && job.Type != normalized.Type {
			continue
		}
		filtered = append(filtered, job)
	}
	total := int64(len(filtered))
	start := (normalized.Page - 1) * normalized.PageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + normalized.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	return frameworkjobs.ListResult{
		Jobs:     append([]frameworkjobs.Job(nil), filtered[start:end]...),
		Page:     normalized.Page,
		PageSize: normalized.PageSize,
		Total:    total,
	}, nil
}

func (queue *sqliteJobAdminQueue) lastListRequest() frameworkjobs.ListRequest {
	queue.state.mu.Lock()
	defer queue.state.mu.Unlock()
	return queue.state.lastList
}

func (queue *sqliteJobAdminQueue) RetryDead(ctx context.Context, id string) error {
	normalizedID, err := frameworkjobs.NormalizeJobID(id)
	if err != nil {
		return err
	}
	var state frameworkjobs.State
	err = queue.db.WithContext(ctx).Raw(
		"SELECT state FROM job_admin_test_states WHERE id = ?",
		normalizedID,
	).Row().Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return frameworkjobs.ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != frameworkjobs.StateDead {
		return frameworkjobs.ErrInvalidTransition
	}
	result := queue.db.WithContext(ctx).Exec(
		"UPDATE job_admin_test_states SET state = ? WHERE id = ? AND state = ?",
		frameworkjobs.StatePending,
		normalizedID,
		frameworkjobs.StateDead,
	)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return frameworkjobs.ErrInvalidTransition
	}
	return nil
}

func (*sqliteJobAdminQueue) Enqueue(
	context.Context,
	frameworkjobs.EnqueueRequest,
) (frameworkjobs.EnqueueResult, error) {
	return frameworkjobs.EnqueueResult{}, nil
}

func (*sqliteJobAdminQueue) Claim(
	context.Context,
	frameworkjobs.ClaimRequest,
) ([]frameworkjobs.Job, error) {
	return []frameworkjobs.Job{}, nil
}

func (*sqliteJobAdminQueue) Heartbeat(context.Context, string, string) error {
	return nil
}

func (*sqliteJobAdminQueue) Succeed(context.Context, string, string) error {
	return nil
}

func (*sqliteJobAdminQueue) Fail(
	context.Context,
	string,
	string,
	error,
) (frameworkjobs.State, error) {
	return frameworkjobs.StateFailed, nil
}
