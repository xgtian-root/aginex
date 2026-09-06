package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/framework/jobs"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/filecleanup"
	"gorm.io/gorm"
)

func TestFileResponsesExposeOnlyPublicDTOFields(t *testing.T) {
	cfg, _, server, cookie := newFileHandlerTestApp(t)
	image := validPNG(t)
	prepared, intentRecorder := createPendingLocalUpload(t, server, cookie, image)

	var intentPayload struct {
		File map[string]any `json:"file"`
	}
	if err := json.Unmarshal(intentRecorder.Body.Bytes(), &intentPayload); err != nil {
		t.Fatal(err)
	}
	assertPublicFileFields(t, "upload intent", intentPayload.File)

	uploadPendingLocalObject(t, server, cookie, cfg, prepared, image)
	confirmRecorder := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if confirmRecorder.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirmRecorder.Code, confirmRecorder.Body.String())
	}
	var confirmed map[string]any
	if err := json.Unmarshal(confirmRecorder.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	assertPublicFileFields(t, "upload confirmation", confirmed)

	listRecorder := serveRequest(server, cookie, http.MethodGet, "/api/v1/files", nil, "")
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRecorder.Code, listRecorder.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("list items = %d, want 1", len(page.Items))
	}
	assertPublicFileFields(t, "file list", page.Items[0])
}

func TestUploadIntentRollsBackWhenAuditInsertFails(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	image := validPNG(t)
	body, err := json.Marshal(map[string]any{
		"filename":    "atomic-intent.png",
		"contentType": "image/png",
		"size":        len(image),
		"visibility":  "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		body,
		"application/json",
	)
	if recorder.Code < http.StatusInternalServerError {
		t.Fatalf("intent status = %d, want 5xx; body = %s", recorder.Code, recorder.Body.String())
	}

	var count int64
	if err := db.Model(&domain.FileObject{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("file rows = %d, want 0 after audit failure", count)
	}
}

func TestUploadConfirmationRollsBackWhenAuditInsertFails(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	image := validPNG(t)
	prepared, _ := createPendingLocalUpload(t, server, cookie, image)
	uploadPendingLocalObject(t, server, cookie, cfg, prepared, image)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	recorder := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if recorder.Code < http.StatusInternalServerError {
		t.Fatalf("confirm status = %d, want 5xx; body = %s", recorder.Code, recorder.Body.String())
	}

	var file domain.FileObject
	if err := db.First(&file, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	if file.Status != "pending" || file.ETag != "" {
		t.Fatalf("file status/etag = %q/%q, want pending/empty after audit failure", file.Status, file.ETag)
	}
}

func TestUploadConfirmationAcceptsMalformedPreviewAsOpaqueDownload(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	malformedJPEG := []byte{
		0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
		0x01, 0x02, 0x03, 0x04, 0x05,
	}
	body, err := json.Marshal(map[string]any{
		"filename":    "forged.jpg",
		"contentType": "image/jpeg",
		"size":        len(malformedJPEG),
		"visibility":  "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	intentRecorder := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		body,
		"application/json",
	)
	if intentRecorder.Code != http.StatusCreated {
		t.Fatalf("intent status = %d, body = %s", intentRecorder.Code, intentRecorder.Body.String())
	}
	var prepared UploadIntentResponse
	if err := json.Unmarshal(intentRecorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Upload == nil {
		t.Fatal("single upload intent omitted upload authorization")
	}
	uploadPath := strings.TrimPrefix(prepared.Upload.URL, cfg.HTTP.PublicURL)
	uploadRecorder := serveRequest(
		server,
		cookie,
		http.MethodPut,
		uploadPath,
		malformedJPEG,
		"application/octet-stream",
	)
	if uploadRecorder.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	confirmRecorder := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if confirmRecorder.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirmRecorder.Code, confirmRecorder.Body.String())
	}
	var confirmed FileResponse
	if err := json.Unmarshal(confirmRecorder.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != "ready" ||
		confirmed.ContentType != "application/octet-stream" ||
		confirmed.PreviewKind != "none" ||
		len(confirmed.SHA256) != 64 {
		t.Fatalf("confirmed malformed preview = %#v", confirmed)
	}

	var file domain.FileObject
	if err := db.First(&file, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	if file.Status != "ready" || file.ContentType != "application/octet-stream" ||
		len(file.SHA256) != 64 || file.Width != 0 || file.Height != 0 {
		t.Fatalf("stored malformed preview = %#v", file)
	}
}

func TestFileDeletionPersistsStateAndEnqueuesCleanupWithoutDeletingInline(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	image := validPNG(t)
	prepared, _ := createPendingLocalUpload(t, server, cookie, image)
	uploadPendingLocalObject(t, server, cookie, cfg, prepared, image)
	confirm := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirm.Code, confirm.Body.String())
	}
	queue := &stubTransactionalQueue{}
	server.jobs = queue

	deletion := serveRequest(
		server,
		cookie,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"",
	)
	if deletion.Code != http.StatusAccepted {
		t.Fatalf("delete status = %d, body = %s", deletion.Code, deletion.Body.String())
	}
	var file domain.FileObject
	if err := db.First(&file, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	if file.Status != "deleting" {
		t.Fatalf("file status = %q, want deleting", file.Status)
	}
	if _, err := server.store.Stat(context.Background(), file.ObjectKey); err != nil {
		t.Fatalf("HTTP deletion removed the physical object: %v", err)
	}
	if len(queue.requests) != 1 ||
		queue.requests[0].Type != filecleanup.JobType ||
		queue.requests[0].Version != filecleanup.PayloadVersion3 ||
		queue.requests[0].IdempotencyKey != "file:"+file.ID+":explicit-delete" {
		t.Fatalf("queued cleanup = %#v", queue.requests)
	}
	var cleanupPayload filecleanup.PayloadV2
	if err := json.Unmarshal(queue.requests[0].Payload, &cleanupPayload); err != nil {
		t.Fatal(err)
	}
	if cleanupPayload.Mode != filecleanup.ModeExplicitDelete {
		t.Fatalf("cleanup mode = %q", cleanupPayload.Mode)
	}
	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "files:delete-request", file.ID).
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("delete-request audits = %d, want 1", auditCount)
	}

	repeated := serveRequest(
		server,
		cookie,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"",
	)
	if repeated.Code != http.StatusAccepted || len(queue.requests) != 1 {
		t.Fatalf("repeated delete status/queues = %d/%d", repeated.Code, len(queue.requests))
	}
}

func TestFileDeletionRollsBackStateWhenCleanupEnqueueFails(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	image := validPNG(t)
	prepared, _ := createPendingLocalUpload(t, server, cookie, image)
	uploadPendingLocalObject(t, server, cookie, cfg, prepared, image)
	confirm := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %s", confirm.Code, confirm.Body.String())
	}
	server.jobs = &stubTransactionalQueue{enqueueErr: errors.New("queue unavailable")}

	deletion := serveRequest(
		server,
		cookie,
		http.MethodDelete,
		"/api/v1/files/"+prepared.File.ID,
		nil,
		"",
	)
	if deletion.Code < http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 5xx; body = %s", deletion.Code, deletion.Body.String())
	}
	var file domain.FileObject
	if err := db.First(&file, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	if file.Status != "ready" {
		t.Fatalf("file status = %q, want ready after enqueue rollback", file.Status)
	}
	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "files:delete-request", file.ID).
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("delete-request audits = %d, want 0 after enqueue rollback", auditCount)
	}
}

func newFileHandlerTestApp(t *testing.T) (config.Config, *gorm.DB, *App, *http.Cookie) {
	t.Helper()
	cfg := config.Config{
		Environment: "test",
		HTTP:        config.HTTP{PublicURL: "http://aginex.test"},
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "aginex.db"),
		},
		Session: config.Session{CookieName: "aginex_session", TTL: time.Hour},
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
	server, err := New(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, db, server, loginCookie(t, server)
}

func createPendingLocalUpload(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	image []byte,
) (UploadIntentResponse, *httptest.ResponseRecorder) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"filename":    "private.png",
		"contentType": "image/png",
		"size":        len(image),
		"visibility":  "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		body,
		"application/json",
	)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("intent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var prepared UploadIntentResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Strategy != "single" || prepared.Upload == nil || prepared.Session != nil {
		t.Fatalf("single upload intent = %#v", prepared)
	}
	return prepared, recorder
}

func uploadPendingLocalObject(
	t *testing.T,
	server *App,
	cookie *http.Cookie,
	cfg config.Config,
	prepared UploadIntentResponse,
	image []byte,
) {
	t.Helper()
	if prepared.Upload == nil {
		t.Fatal("single upload intent omitted upload authorization")
	}
	uploadPath := strings.TrimPrefix(prepared.Upload.URL, cfg.HTTP.PublicURL)
	recorder := serveRequest(server, cookie, http.MethodPut, uploadPath, image, "application/octet-stream")
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func assertPublicFileFields(t *testing.T, responseName string, file map[string]any) {
	t.Helper()
	allowed := map[string]struct{}{
		"id":                 {},
		"provider":           {},
		"storageProfileId":   {},
		"storageProfileName": {},
		"storageProvider":    {},
		"originalName":       {},
		"contentType":        {},
		"previewKind":        {},
		"size":               {},
		"sha256":             {},
		"width":              {},
		"height":             {},
		"visibility":         {},
		"status":             {},
		"createdAt":          {},
		"updatedAt":          {},
	}
	for field := range file {
		if _, ok := allowed[field]; !ok {
			t.Errorf("%s exposes private or undocumented file field %q", responseName, field)
		}
	}
	for field := range allowed {
		if _, ok := file[field]; !ok {
			t.Errorf("%s omits public file field %q", responseName, field)
		}
	}
}

type stubTransactionalQueue struct {
	requests   []jobs.EnqueueRequest
	enqueueErr error
}

func (queue *stubTransactionalQueue) Bind(*gorm.DB) (jobs.Queue, error) {
	return queue, nil
}

func (queue *stubTransactionalQueue) Enqueue(
	_ context.Context,
	request jobs.EnqueueRequest,
) (jobs.EnqueueResult, error) {
	if queue.enqueueErr != nil {
		return jobs.EnqueueResult{}, queue.enqueueErr
	}
	queue.requests = append(queue.requests, request)
	return jobs.EnqueueResult{Created: true}, nil
}

func (*stubTransactionalQueue) Claim(context.Context, jobs.ClaimRequest) ([]jobs.Job, error) {
	return []jobs.Job{}, nil
}

func (*stubTransactionalQueue) Heartbeat(context.Context, string, string) error {
	return nil
}

func (*stubTransactionalQueue) Succeed(context.Context, string, string) error {
	return nil
}

func (*stubTransactionalQueue) Fail(context.Context, string, string, error) (jobs.State, error) {
	return jobs.StateFailed, nil
}

func (*stubTransactionalQueue) RetryDead(context.Context, string) error {
	return nil
}
