package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	frameworkauthz "github.com/xgtian-root/aginex/framework/authz"
	frameworkidempotency "github.com/xgtian-root/aginex/framework/idempotency"
	"github.com/xgtian-root/aginex/internal/domain"
	"gorm.io/gorm"
)

func TestProductIdempotencyReplaysAcrossAppInstances(t *testing.T) {
	cfg, db, first, cookie := newFileHandlerTestApp(t)
	second, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(
		`{"name":"Shared Desk","sku":"IDEMPOTENT-SHARED","priceCents":500,"status":"active"}`,
	)

	initial := serveIdempotentRequest(
		first,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
		"shared-create-key",
	)
	if initial.Code != http.StatusCreated {
		t.Fatalf("initial status = %d, body = %s", initial.Code, initial.Body.String())
	}
	replayed := serveIdempotentRequest(
		second,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
		"shared-create-key",
	)
	if replayed.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, body = %s", replayed.Code, replayed.Body.String())
	}
	if replayed.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf("replay header = %q", replayed.Header().Get(idempotencyReplayedHeader))
	}
	if initial.Body.String() != replayed.Body.String() {
		t.Fatalf("replayed body = %s, want %s", replayed.Body.String(), initial.Body.String())
	}
	if initial.Header().Get("Location") != replayed.Header().Get("Location") {
		t.Fatalf(
			"replayed location = %q, want %q",
			replayed.Header().Get("Location"),
			initial.Header().Get("Location"),
		)
	}
	assertProductAndAuditCounts(t, db, "IDEMPOTENT-SHARED", 1, 1)
}

func TestProductIdempotencyRejectsSameKeyForDifferentRequest(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	firstBody := []byte(
		`{"name":"Original","sku":"IDEMPOTENT-ORIGINAL","priceCents":100,"status":"active"}`,
	)
	conflictingBody := []byte(
		`{"name":"Changed","sku":"IDEMPOTENT-CHANGED","priceCents":200,"status":"active"}`,
	)
	first := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		firstBody,
		"request-binding-key",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	conflict := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		conflictingBody,
		"request-binding-key",
	)
	assertProblemCode(
		t,
		conflict,
		http.StatusConflict,
		"IDEMPOTENCY_KEY_REUSED",
	)
	assertProductAndAuditCounts(t, db, "IDEMPOTENT-ORIGINAL", 1, 1)
	assertProductAndAuditCounts(t, db, "IDEMPOTENT-CHANGED", 0, 0)
}

func TestProductIdempotencyScopesTheSameKeyByActor(t *testing.T) {
	_, db, server, adminCookie := newFileHandlerTestApp(t)
	const passwordValue = "correct horse battery staple"
	createFileUser(
		t,
		db,
		"idempotency-user@example.com",
		passwordValue,
		frameworkauthz.ScopeOwn,
	)
	grantProductCreate(t, db, "idempotency-user@example.com")
	userCookie := loginCookieAs(
		t,
		server,
		"idempotency-user@example.com",
		passwordValue,
	)

	admin := serveIdempotentRequest(
		server,
		adminCookie,
		http.MethodPost,
		"/api/v1/products",
		[]byte(`{"name":"Admin Product","sku":"ACTOR-ADMIN","priceCents":1,"status":"active"}`),
		"actor-isolated-key",
	)
	if admin.Code != http.StatusCreated {
		t.Fatalf("admin status = %d, body = %s", admin.Code, admin.Body.String())
	}
	user := serveIdempotentRequest(
		server,
		userCookie,
		http.MethodPost,
		"/api/v1/products",
		[]byte(`{"name":"User Product","sku":"ACTOR-USER","priceCents":2,"status":"active"}`),
		"actor-isolated-key",
	)
	if user.Code != http.StatusCreated {
		t.Fatalf("user status = %d, body = %s", user.Code, user.Body.String())
	}
	assertProductAndAuditCounts(t, db, "ACTOR-ADMIN", 1, 1)
	assertProductAndAuditCounts(t, db, "ACTOR-USER", 1, 1)
}

func TestProductUpdateAndDeleteUseBoundPathAndReplay(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	now := time.Now().UTC()
	firstProduct := domain.Product{
		ID: uuid.NewString(), Name: "First", SKU: "PATH-FIRST",
		PriceCents: 1, Status: "draft", CreatedAt: now, UpdatedAt: now,
	}
	secondProduct := domain.Product{
		ID: uuid.NewString(), Name: "Second", SKU: "PATH-SECOND",
		PriceCents: 2, Status: "draft", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&[]domain.Product{firstProduct, secondProduct}).Error; err != nil {
		t.Fatal(err)
	}
	updateBody := []byte(
		`{"name":"Updated","sku":"PATH-FIRST-UPDATED","priceCents":3,"status":"active"}`,
	)
	updated := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPut,
		"/api/v1/products/"+firstProduct.ID,
		updateBody,
		"bound-update-key",
	)
	if updated.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", updated.Code, updated.Body.String())
	}
	updateReplay := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPut,
		"/api/v1/products/"+firstProduct.ID,
		updateBody,
		"bound-update-key",
	)
	if updateReplay.Code != http.StatusOK ||
		updateReplay.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf(
			"update replay status/header = %d/%q, body = %s",
			updateReplay.Code,
			updateReplay.Header().Get(idempotencyReplayedHeader),
			updateReplay.Body.String(),
		)
	}
	pathConflict := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPut,
		"/api/v1/products/"+secondProduct.ID,
		updateBody,
		"bound-update-key",
	)
	assertProblemCode(
		t,
		pathConflict,
		http.StatusConflict,
		"IDEMPOTENCY_KEY_REUSED",
	)
	var updateAudits int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "products:update", firstProduct.ID).
		Count(&updateAudits).Error; err != nil {
		t.Fatal(err)
	}
	if updateAudits != 1 {
		t.Fatalf("update audits = %d, want 1", updateAudits)
	}

	deleted := serveIdempotentRequest(
		server,
		cookie,
		http.MethodDelete,
		"/api/v1/products/"+firstProduct.ID,
		nil,
		"delete-product-key",
	)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	deleteReplay := serveIdempotentRequest(
		server,
		cookie,
		http.MethodDelete,
		"/api/v1/products/"+firstProduct.ID,
		nil,
		"delete-product-key",
	)
	if deleteReplay.Code != http.StatusNoContent ||
		deleteReplay.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf(
			"delete replay status/header = %d/%q",
			deleteReplay.Code,
			deleteReplay.Header().Get(idempotencyReplayedHeader),
		)
	}
	var remaining int64
	if err := db.Model(&domain.Product{}).
		Where("id = ?", firstProduct.ID).
		Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("deleted product rows = %d, want 0", remaining)
	}
	var deleteAudits int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "products:delete", firstProduct.ID).
		Count(&deleteAudits).Error; err != nil {
		t.Fatal(err)
	}
	if deleteAudits != 1 {
		t.Fatalf("delete audits = %d, want 1", deleteAudits)
	}
}

func TestConcurrentIdempotentProductCreateHasOneBusinessWinner(t *testing.T) {
	cfg, db, first, cookie := newFileHandlerTestApp(t)
	second, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)

	const callers = 24
	body := []byte(
		`{"name":"Concurrent","sku":"IDEMPOTENT-CONCURRENT","priceCents":50,"status":"active"}`,
	)
	start := make(chan struct{})
	recorders := make(chan *httptest.ResponseRecorder, callers)
	var group sync.WaitGroup
	for index := range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			server := first
			if index%2 == 0 {
				server = second
			}
			recorders <- serveIdempotentRequest(
				server,
				cookie,
				http.MethodPost,
				"/api/v1/products",
				body,
				"concurrent-create-key",
			)
		}()
	}
	close(start)
	group.Wait()
	close(recorders)

	for recorder := range recorders {
		if recorder.Code != http.StatusCreated && recorder.Code != http.StatusConflict {
			t.Errorf(
				"concurrent status = %d, want 201 or 409; body = %s",
				recorder.Code,
				recorder.Body.String(),
			)
		}
	}
	assertProductAndAuditCounts(t, db, "IDEMPOTENT-CONCURRENT", 1, 1)
}

func TestIdempotencyRejectsInvalidAndDuplicateHeaderValues(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	body := []byte(
		`{"name":"Invalid Key","sku":"INVALID-KEY","priceCents":10,"status":"active"}`,
	)
	tooLong := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
		strings.Repeat("x", frameworkidempotency.MaxKeyBytes+1),
	)
	assertProblemCode(
		t,
		tooLong,
		http.StatusBadRequest,
		"IDEMPOTENCY_KEY_INVALID",
	)

	request := authenticatedWriteRequest(
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
	)
	request.Header.Add(idempotencyHeader, "first")
	request.Header.Add(idempotencyHeader, "second")
	duplicate := httptest.NewRecorder()
	server.Handler().ServeHTTP(duplicate, request)
	assertProblemCode(
		t,
		duplicate,
		http.StatusBadRequest,
		"IDEMPOTENCY_KEY_INVALID",
	)
	assertProductAndAuditCounts(t, db, "INVALID-KEY", 0, 0)
}

func TestFailedIdempotentWriteIsAbandonedAndCanRetry(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	now := time.Now().UTC()
	blocker := domain.Product{
		ID: uuid.NewString(), Name: "Blocker", SKU: "IDEMPOTENT-RETRY",
		PriceCents: 1, Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&blocker).Error; err != nil {
		t.Fatal(err)
	}
	body := []byte(
		`{"name":"Retry Product","sku":"IDEMPOTENT-RETRY","priceCents":75,"status":"active"}`,
	)
	failed := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
		"retry-after-5xx",
	)
	if failed.Code < http.StatusInternalServerError {
		t.Fatalf("failed status = %d, body = %s", failed.Code, failed.Body.String())
	}
	var claims int64
	if err := db.Table(frameworkidempotency.TableName).Count(&claims).Error; err != nil {
		t.Fatal(err)
	}
	if claims != 0 {
		t.Fatalf("idempotency claims after 5xx = %d, want 0", claims)
	}
	if err := db.Delete(&blocker).Error; err != nil {
		t.Fatal(err)
	}

	retried := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
		"retry-after-5xx",
	)
	if retried.Code != http.StatusCreated {
		t.Fatalf("retried status = %d, body = %s", retried.Code, retried.Body.String())
	}
	assertProductAndAuditCounts(t, db, "IDEMPOTENT-RETRY", 1, 1)
}

func TestUploadIntentReplayDoesNotPersistSignedRequest(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	body := []byte(
		`{"filename":"replay.png","contentType":"image/png","size":128,"visibility":"private"}`,
	)
	first := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		body,
		"safe-upload-intent",
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", first.Code, first.Body.String())
	}
	var firstPayload UploadIntentResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstPayload); err != nil {
		t.Fatal(err)
	}
	if firstPayload.Upload == nil || firstPayload.Upload.URL == "" {
		t.Fatal("first upload intent has an empty URL")
	}

	var stored string
	if err := db.Raw(
		"SELECT response_body FROM "+frameworkidempotency.TableName+" WHERE route = ?",
		"/api/v1/files/upload-intents",
	).Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, firstPayload.Upload.URL) ||
		strings.Contains(stored, "/local-upload/") {
		t.Fatalf("stored replay contains upload URL: %s", stored)
	}

	replayed := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		body,
		"safe-upload-intent",
	)
	if replayed.Code != http.StatusCreated {
		t.Fatalf("replay status = %d, body = %s", replayed.Code, replayed.Body.String())
	}
	if replayed.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf("replay header = %q", replayed.Header().Get(idempotencyReplayedHeader))
	}
	var replayedPayload UploadIntentResponse
	if err := json.Unmarshal(replayed.Body.Bytes(), &replayedPayload); err != nil {
		t.Fatal(err)
	}
	if replayedPayload.File.ID != firstPayload.File.ID ||
		replayedPayload.Upload == nil || replayedPayload.Upload.URL == "" {
		t.Fatalf("replayed upload intent = %#v", replayedPayload)
	}
	if replayedPayload.Upload.ExpiresAt.After(firstPayload.Upload.ExpiresAt) {
		t.Fatalf("replayed upload expiry = %s, exceeds original %s", replayedPayload.Upload.ExpiresAt, firstPayload.Upload.ExpiresAt)
	}
	var fileCount int64
	if err := db.Model(&domain.FileObject{}).Count(&fileCount).Error; err != nil {
		t.Fatal(err)
	}
	if fileCount != 1 {
		t.Fatalf("file count = %d, want 1", fileCount)
	}
	expired := time.Now().UTC().Add(-time.Second)
	if err := db.Model(&domain.FileObject{}).
		Where("id = ?", firstPayload.File.ID).
		Update("upload_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	expiredReplay := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		body,
		"safe-upload-intent",
	)
	assertProblemCode(t, expiredReplay, http.StatusConflict, "UPLOAD_INTENT_EXPIRED")
}

func TestFileConfirmationAndDeletionReplayWithoutDuplicateSideEffects(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	image := validPNG(t)
	prepared, _ := createPendingLocalUpload(t, server, cookie, image)
	uploadPendingLocalObject(t, server, cookie, cfg, prepared, image)

	confirmed := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"confirm-file-key",
	)
	if confirmed.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirmed.Code, confirmed.Body.String())
	}
	confirmReplay := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"confirm-file-key",
	)
	if confirmReplay.Code != http.StatusOK ||
		confirmReplay.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf(
			"confirm replay status/header = %d/%q, body = %s",
			confirmReplay.Code,
			confirmReplay.Header().Get(idempotencyReplayedHeader),
			confirmReplay.Body.String(),
		)
	}
	var confirmAudits int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "files:confirm", prepared.File.ID).
		Count(&confirmAudits).Error; err != nil {
		t.Fatal(err)
	}
	if confirmAudits != 1 {
		t.Fatalf("confirmation audits = %d, want 1", confirmAudits)
	}

	queue := &stubTransactionalQueue{}
	server.jobs = queue
	deleted := serveIdempotentRequest(
		server,
		cookie,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"delete-file-key",
	)
	if deleted.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d, body = %s", deleted.Code, deleted.Body.String())
	}
	deleteReplay := serveIdempotentRequest(
		server,
		cookie,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"delete-file-key",
	)
	if deleteReplay.Code != http.StatusAccepted ||
		deleteReplay.Header().Get(idempotencyReplayedHeader) != "true" {
		t.Fatalf(
			"delete replay status/header = %d/%q, body = %s",
			deleteReplay.Code,
			deleteReplay.Header().Get(idempotencyReplayedHeader),
			deleteReplay.Body.String(),
		)
	}
	if len(queue.requests) != 1 {
		t.Fatalf("cleanup enqueues = %d, want 1", len(queue.requests))
	}
	var deleteAudits int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "files:delete-request", prepared.File.ID).
		Count(&deleteAudits).Error; err != nil {
		t.Fatal(err)
	}
	if deleteAudits != 1 {
		t.Fatalf("delete-request audits = %d, want 1", deleteAudits)
	}
}

func TestDisabledIdempotencyRejectsRatherThanIgnoresHeader(t *testing.T) {
	cfg, db, _, cookie := newFileHandlerTestApp(t)
	cfg.Idempotency.Driver = "disabled"
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(
		`{"name":"Disabled","sku":"IDEMPOTENCY-DISABLED","priceCents":10,"status":"active"}`,
	)
	rejected := serveIdempotentRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
		"must-not-be-ignored",
	)
	assertProblemCode(
		t,
		rejected,
		http.StatusServiceUnavailable,
		"IDEMPOTENCY_UNAVAILABLE",
	)
	assertProductAndAuditCounts(t, db, "IDEMPOTENCY-DISABLED", 0, 0)

	withoutHeader := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/products",
		body,
		"application/json",
	)
	if withoutHeader.Code != http.StatusCreated {
		t.Fatalf(
			"header-free request status = %d, body = %s",
			withoutHeader.Code,
			withoutHeader.Body.String(),
		)
	}
}

func TestIdempotencySchemaIsCheckedAtStartupAndReadiness(t *testing.T) {
	cfg, db, server, _ := newFileHandlerTestApp(t)
	if err := db.Exec(
		"UPDATE " + frameworkidempotency.MigrationTableName +
			" SET is_applied = false WHERE version_id = 1",
	).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := New(cfg, db); !errors.Is(
		err,
		frameworkidempotency.ErrSchemaNotCurrent,
	) {
		t.Fatalf("startup error = %v, want ErrSchemaNotCurrent", err)
	}
	ready := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		ready,
		httptest.NewRequest(http.MethodGet, "/api/v1/health/ready", nil),
	)
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, body = %s", ready.Code, ready.Body.String())
	}
	live := httptest.NewRecorder()
	server.Handler().ServeHTTP(
		live,
		httptest.NewRequest(http.MethodGet, "/api/v1/health/live", nil),
	)
	if live.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, body = %s", live.Code, live.Body.String())
	}
}

func serveIdempotentRequest(
	server *App,
	cookie *http.Cookie,
	method string,
	path string,
	body []byte,
	key string,
) *httptest.ResponseRecorder {
	request := authenticatedWriteRequest(cookie, method, path, body)
	request.Header.Set(idempotencyHeader, key)
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)
	return recorder
}

func authenticatedWriteRequest(
	cookie *http.Cookie,
	method string,
	path string,
	body []byte,
) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewReader(body))
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	addTestCSRF(request)
	return request
}

func assertProductAndAuditCounts(
	t *testing.T,
	db *gorm.DB,
	sku string,
	wantProducts int64,
	wantAudits int64,
) {
	t.Helper()
	var products int64
	if err := db.Model(&domain.Product{}).Where("sku = ?", sku).Count(&products).Error; err != nil {
		t.Fatal(err)
	}
	if products != wantProducts {
		t.Fatalf("products for %s = %d, want %d", sku, products, wantProducts)
	}
	var audits int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND sanitized_after LIKE ?", "products:create", "%"+sku+"%").
		Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != wantAudits {
		t.Fatalf("audits for %s = %d, want %d", sku, audits, wantAudits)
	}
}

func grantProductCreate(t *testing.T, db *gorm.DB, email string) {
	t.Helper()
	var user domain.User
	if err := db.First(&user, "email = ?", email).Error; err != nil {
		t.Fatal(err)
	}
	var permission domain.Permission
	if err := db.First(&permission, "code = ?", "products:create").Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	role := domain.Role{
		ID: uuid.NewString(), Name: "product-create-" + user.ID,
		Description: "Product idempotency fixture", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&role).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO role_permissions (role_id, permission_id, scope) VALUES (?, ?, ?)",
		role.ID,
		permission.ID,
		frameworkauthz.ScopeAll,
	).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)",
		user.ID,
		role.ID,
	).Error; err != nil {
		t.Fatal(err)
	}
}
