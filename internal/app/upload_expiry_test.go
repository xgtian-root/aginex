package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/framework/jobs"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/filecleanup"
	"gorm.io/gorm"
)

func TestUploadIntentEnqueuesPendingExpiryInsideWriteTransaction(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	queue := &expiryRecordingQueue{persist: true}
	server.jobs = queue
	if err := db.Exec(`
		CREATE TABLE test_expiry_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}

	image := validPNG(t)
	prepared, recorder := createPendingLocalUpload(t, server, cookie, image)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("intent status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !queue.boundInTransaction {
		t.Fatal("expiry enqueue was not bound to the business transaction")
	}
	if len(queue.requests) != 1 {
		t.Fatalf("queued requests = %d, want 1", len(queue.requests))
	}
	request := queue.requests[0]
	if request.Type != filecleanup.JobType ||
		request.Version != filecleanup.PayloadVersion2 ||
		request.IdempotencyKey != "file:"+prepared.File.ID+":pending-expiry" {
		t.Fatalf("expiry request = %#v", request)
	}
	wantSchedule := prepared.Upload.ExpiresAt.Add(pendingUploadCleanupGrace)
	if !request.ScheduledAt.Equal(wantSchedule) {
		t.Fatalf("scheduled at = %s, want %s", request.ScheduledAt, wantSchedule)
	}
	var payload filecleanup.PayloadV2
	if err := json.Unmarshal(request.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	var file domain.FileObject
	if err := db.First(&file, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	if payload.FileID != file.ID ||
		payload.Provider != file.Provider ||
		payload.ObjectKey != file.ObjectKey ||
		payload.Mode != filecleanup.ModePendingExpiry {
		t.Fatalf("expiry payload = %#v, file = %#v", payload, file)
	}
	if request.CreatedBy.ID != file.OwnerID || request.CreatedBy.Kind != "user" {
		t.Fatalf("expiry creator = %#v", request.CreatedBy)
	}
	var persisted int64
	if err := db.Table("test_expiry_jobs").Count(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	if persisted != 1 {
		t.Fatalf("persisted expiry jobs = %d, want 1", persisted)
	}
}

func TestUploadIntentRollsBackExpiryEnqueueWithAuditFailure(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	queue := &expiryRecordingQueue{persist: true}
	server.jobs = queue
	if err := db.Exec(`
		CREATE TABLE test_expiry_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			idempotency_key TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	image := validPNG(t)
	body, err := json.Marshal(map[string]any{
		"filename":    "rollback-expiry.png",
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
	if len(queue.requests) != 1 || !queue.boundInTransaction {
		t.Fatalf("expiry enqueue calls/transaction = %d/%v", len(queue.requests), queue.boundInTransaction)
	}
	var fileCount int64
	if err := db.Model(&domain.FileObject{}).Count(&fileCount).Error; err != nil {
		t.Fatal(err)
	}
	var jobCount int64
	if err := db.Table("test_expiry_jobs").Count(&jobCount).Error; err != nil {
		t.Fatal(err)
	}
	if fileCount != 0 || jobCount != 0 {
		t.Fatalf("rolled-back files/jobs = %d/%d, want 0/0", fileCount, jobCount)
	}
}

func TestInvalidUploadCleanupCannotCollideWithPendingExpiry(t *testing.T) {
	cfg, _, server, cookie := newFileHandlerTestApp(t)
	queue := &expiryRecordingQueue{}
	server.jobs = queue
	image := validPNG(t)
	body, err := json.Marshal(map[string]any{
		"filename":    "forged.jpg",
		"contentType": "image/jpeg",
		"size":        len(image),
		"visibility":  "private",
	})
	if err != nil {
		t.Fatal(err)
	}
	intent := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/upload-intents",
		body,
		"application/json",
	)
	if intent.Code != http.StatusCreated {
		t.Fatalf("intent status = %d, body = %s", intent.Code, intent.Body.String())
	}
	var prepared UploadIntentResponse
	if err := json.Unmarshal(intent.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	uploadPath := prepared.Upload.URL[len(cfg.HTTP.PublicURL):]
	upload := serveRequest(server, cookie, http.MethodPut, uploadPath, image, "image/jpeg")
	if upload.Code != http.StatusNoContent {
		t.Fatalf("upload status = %d, body = %s", upload.Code, upload.Body.String())
	}
	confirm := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	if confirm.Code != http.StatusUnprocessableEntity {
		t.Fatalf("confirm status = %d, body = %s", confirm.Code, confirm.Body.String())
	}
	if len(queue.requests) != 2 {
		t.Fatalf("queued requests = %d, want expiry and invalid cleanup", len(queue.requests))
	}
	expiry := queue.requests[0]
	invalid := queue.requests[1]
	if expiry.IdempotencyKey != "file:"+prepared.File.ID+":pending-expiry" ||
		invalid.IdempotencyKey != "file:"+prepared.File.ID+":invalid-delete" ||
		expiry.IdempotencyKey == invalid.IdempotencyKey {
		t.Fatalf("cleanup idempotency keys = %q / %q", expiry.IdempotencyKey, invalid.IdempotencyKey)
	}
	var invalidPayload filecleanup.PayloadV2
	if err := json.Unmarshal(invalid.Payload, &invalidPayload); err != nil {
		t.Fatal(err)
	}
	if invalidPayload.Mode != filecleanup.ModeExplicitDelete {
		t.Fatalf("invalid cleanup mode = %q", invalidPayload.Mode)
	}
}

func TestConfirmUploadDoesNotResurrectIntentAfterStateRace(t *testing.T) {
	cfg, db, server, cookie := newFileHandlerTestApp(t)
	image := validPNG(t)
	prepared, _ := createPendingLocalUpload(t, server, cookie, image)
	uploadPendingLocalObject(t, server, cookie, cfg, prepared, image)
	deletedAt := time.Now().UTC()
	if err := db.Model(&domain.FileObject{}).
		Where("id = ?", prepared.File.ID).
		Updates(map[string]any{"status": "deleted", "deleted_at": deletedAt}).Error; err != nil {
		t.Fatal(err)
	}

	confirm := serveRequest(
		server,
		cookie,
		http.MethodPost,
		"/api/v1/files/"+prepared.File.ID+"/confirm",
		nil,
		"",
	)
	assertProblemCode(t, confirm, http.StatusConflict, "REQUEST_CONFLICT")
	var stored domain.FileObject
	if err := db.First(&stored, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "deleted" {
		t.Fatalf("file status after raced confirmation = %q, want deleted", stored.Status)
	}
	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ? AND resource_id = ?", "files:confirm", prepared.File.ID).
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("confirm audits after state race = %d, want 0", auditCount)
	}
}

type expiryRecordingQueue struct {
	requests           []jobs.EnqueueRequest
	boundInTransaction bool
	persist            bool
}

func (queue *expiryRecordingQueue) Bind(tx *gorm.DB) (jobs.Queue, error) {
	_, queue.boundInTransaction = tx.Statement.ConnPool.(*sql.Tx)
	return &boundExpiryRecordingQueue{parent: queue, db: tx}, nil
}

func (queue *expiryRecordingQueue) Enqueue(
	ctx context.Context,
	request jobs.EnqueueRequest,
) (jobs.EnqueueResult, error) {
	return (&boundExpiryRecordingQueue{parent: queue}).Enqueue(ctx, request)
}

func (*expiryRecordingQueue) Claim(context.Context, jobs.ClaimRequest) ([]jobs.Job, error) {
	return nil, nil
}

func (*expiryRecordingQueue) Heartbeat(context.Context, string, string) error {
	return nil
}

func (*expiryRecordingQueue) Succeed(context.Context, string, string) error {
	return nil
}

func (*expiryRecordingQueue) Fail(context.Context, string, string, error) (jobs.State, error) {
	return jobs.StateFailed, nil
}

func (*expiryRecordingQueue) RetryDead(context.Context, string) error {
	return nil
}

type boundExpiryRecordingQueue struct {
	parent *expiryRecordingQueue
	db     *gorm.DB
}

func (queue *boundExpiryRecordingQueue) Enqueue(
	ctx context.Context,
	request jobs.EnqueueRequest,
) (jobs.EnqueueResult, error) {
	if err := request.Normalize(time.Now().UTC()); err != nil {
		return jobs.EnqueueResult{}, err
	}
	queue.parent.requests = append(queue.parent.requests, request)
	if queue.parent.persist {
		if queue.db == nil {
			return jobs.EnqueueResult{}, jobs.ErrInvalid
		}
		if err := queue.db.WithContext(ctx).Exec(
			"INSERT INTO test_expiry_jobs (idempotency_key) VALUES (?)",
			request.IdempotencyKey,
		).Error; err != nil {
			return jobs.EnqueueResult{}, err
		}
	}
	return jobs.EnqueueResult{Created: true}, nil
}

func (*boundExpiryRecordingQueue) Claim(context.Context, jobs.ClaimRequest) ([]jobs.Job, error) {
	return nil, nil
}

func (*boundExpiryRecordingQueue) Heartbeat(context.Context, string, string) error {
	return nil
}

func (*boundExpiryRecordingQueue) Succeed(context.Context, string, string) error {
	return nil
}

func (*boundExpiryRecordingQueue) Fail(
	context.Context,
	string,
	string,
	error,
) (jobs.State, error) {
	return jobs.StateFailed, nil
}

func (*boundExpiryRecordingQueue) RetryDead(context.Context, string) error {
	return nil
}
