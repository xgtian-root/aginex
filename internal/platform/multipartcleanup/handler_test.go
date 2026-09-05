package multipartcleanup

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/jobs"
	"github.com/xgtian-root/aginex/framework/observability"
	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/database"
	"github.com/xgtian-root/aginex/internal/platform/migrate"
	"github.com/xgtian-root/aginex/internal/platform/storage"
	"gorm.io/gorm"
)

var cleanupTestNow = time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

func TestHandleExpiresMultipartSessionWithAuditedCAS(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	file, session := seedUploadSession(t, db, registry, profileID, domain.FileUploadSessionStatusActive, cleanupTestNow.Add(-time.Minute), "pending", true)
	handler := cleanupTestHandler(t, db, registry)

	raw, err := json.Marshal(Payload{SessionID: session.ID, Cause: CauseExpiry})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.Handle(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	assertCleanupTerminalState(t, db, file.ID, session.ID, "deleted", domain.FileUploadSessionStatusExpired)
	assertCleanupAudits(t, db, session.ID, 2)

	if err := handler.Handle(context.Background(), raw); err != nil {
		t.Fatalf("repeated cleanup = %v", err)
	}
	assertCleanupAudits(t, db, session.ID, 2)
}

func TestHandleLeavesCompletedOrReadySessionAndProviderUploadUntouched(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	_, session := seedUploadSession(t, db, registry, profileID, domain.FileUploadSessionStatusActive, cleanupTestNow.Add(-time.Minute), "ready", true)
	handler := cleanupTestHandler(t, db, registry)

	if err := handler.Cleanup(context.Background(), session.ID, CauseExpiry); err != nil {
		t.Fatal(err)
	}
	var stored domain.FileUploadSession
	if err := db.First(&stored, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.FileUploadSessionStatusActive {
		t.Fatalf("session status = %q", stored.Status)
	}
	multipart, err := registry.ResolveMultipart(profileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := multipart.ListUploadedParts(context.Background(), frameworkstorage.MultipartUpload{
		Key: "uploads/" + session.FileID, ProviderUploadID: session.ProviderUploadID,
	}); err != nil {
		t.Fatalf("stale cleanup aborted a ready file's provider upload: %v", err)
	}
}

func TestHandleRollsBackStateWhenAuditCannotCommit(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	_, session := seedUploadSession(t, db, registry, profileID, domain.FileUploadSessionStatusActive, cleanupTestNow.Add(-time.Minute), "pending", true)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}
	handler := cleanupTestHandler(t, db, registry)

	err := handler.Cleanup(context.Background(), session.ID, CauseExpiry)
	if err == nil {
		t.Fatal("cleanup unexpectedly succeeded without audit storage")
	}
	var stored domain.FileUploadSession
	if err := db.First(&stored, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.FileUploadSessionStatusActive {
		t.Fatalf("unaudited transition persisted status %q", stored.Status)
	}
}

func TestHandleRejectsEarlyExpiryAndStrictPayload(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	_, session := seedUploadSession(t, db, registry, profileID, domain.FileUploadSessionStatusActive, cleanupTestNow.Add(time.Minute), "pending", false)
	handler := cleanupTestHandler(t, db, registry)
	if err := handler.Cleanup(context.Background(), session.ID, CauseExpiry); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("early expiry = %v, want ErrUnsafeState", err)
	}

	cases := []json.RawMessage{
		json.RawMessage(`{"sessionId":"` + session.ID + `","cause":"expiry","providerUploadId":"secret"}`),
		json.RawMessage(`{"sessionId":"` + session.ID + `","cause":"delete"}`),
		json.RawMessage(`{"sessionId":"` + session.ID + `","cause":"expiry"} {}`),
	}
	for _, raw := range cases {
		if err := handler.Handle(context.Background(), raw); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("payload %s: error = %v", raw, err)
		}
	}
}

func TestNewEnqueueRequestContainsNoProviderCapability(t *testing.T) {
	sessionID := uuid.NewString()
	request, err := NewEnqueueRequest(
		sessionID,
		CauseExpiry,
		cleanupTestNow,
		authz.NewSystemActor("api-scanner"),
		jobs.TraceContext{RequestID: "request-1"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.Type != JobType || request.Version != PayloadVersion ||
		request.IdempotencyKey != "multipart-session:"+sessionID+":expiry" {
		t.Fatalf("request = %#v", request)
	}
	if strings.Contains(string(request.Payload), "provider") || strings.Contains(string(request.Payload), "etag") {
		t.Fatalf("sensitive capability leaked into payload: %s", request.Payload)
	}
}

func cleanupTestRuntime(t *testing.T) (*gorm.DB, *storage.Registry, string) {
	t.Helper()
	db, err := database.Open(config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "multipart-cleanup.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrate.Up(sqlDB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := applyMultipartCleanupFixture(db); err != nil {
		t.Fatal(err)
	}
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := frameworkstorage.NewFilePolicy(frameworkstorage.AbsoluteMaxFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.WithDefaults(config.Config{
		Storage: config.Storage{Driver: "local", LocalRoot: t.TempDir()},
		HTTP:    config.HTTP{PublicURL: "http://localhost:8080"},
	})
	registry, err := storage.NewRegistry(context.Background(), cfg, recorder, policy)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := registry.ActiveProfile()
	if !ok {
		t.Fatal("active storage profile is missing")
	}
	return db, registry, profile.ID
}

func cleanupTestHandler(t *testing.T, db *gorm.DB, registry *storage.Registry) *Handler {
	t.Helper()
	handler, err := New(db, registry, Config{
		SystemActorID: "multipart-cleanup-test",
		Clock:         func() time.Time { return cleanupTestNow },
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func seedUploadSession(
	t *testing.T,
	db *gorm.DB,
	registry *storage.Registry,
	profileID string,
	status domain.FileUploadSessionStatus,
	expiresAt time.Time,
	fileStatus string,
	createProviderUpload bool,
) (domain.FileObject, domain.FileUploadSession) {
	t.Helper()
	return seedUploadSessionSized(
		t,
		db,
		registry,
		profileID,
		status,
		expiresAt,
		fileStatus,
		createProviderUpload,
		64<<20,
	)
}

func seedUploadSessionSized(
	t *testing.T,
	db *gorm.DB,
	registry *storage.Registry,
	profileID string,
	status domain.FileUploadSessionStatus,
	expiresAt time.Time,
	fileStatus string,
	createProviderUpload bool,
	fileSize int64,
) (domain.FileObject, domain.FileUploadSession) {
	t.Helper()
	user := domain.User{
		ID: uuid.NewString(), Email: uuid.NewString() + "@example.com",
		DisplayName: "Upload owner", Status: "active",
		CreatedAt: cleanupTestNow.Add(-time.Hour), UpdatedAt: cleanupTestNow.Add(-time.Hour),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	profile, ok := registry.Profile(profileID)
	if !ok {
		t.Fatal("storage profile is missing")
	}
	file := domain.FileObject{
		ID: uuid.NewString(), StorageProfileID: &profileID,
		Provider: profile.StorageConfig().Driver, Bucket: profile.StorageConfig().Bucket,
		OriginalName: "archive.zip", ContentType: frameworkstorage.StoredContentType,
		Size: fileSize, OwnerID: user.ID, Visibility: "private", Status: fileStatus,
		CreatedAt: cleanupTestNow.Add(-time.Hour), UpdatedAt: cleanupTestNow.Add(-time.Hour),
	}
	file.ObjectKey = "uploads/" + file.ID
	upload := frameworkstorage.MultipartUpload{Key: file.ObjectKey, ProviderUploadID: uuid.NewString()}
	if createProviderUpload {
		multipart, err := registry.ResolveMultipart(profileID)
		if err != nil {
			t.Fatal(err)
		}
		upload, err = multipart.InitiateMultipart(context.Background(), frameworkstorage.UploadRequest{
			Key: file.ObjectKey, ContentType: frameworkstorage.StoredContentType,
			Size: file.Size, Expires: time.Hour,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	session := domain.FileUploadSession{
		ID: uuid.NewString(), FileID: file.ID,
		ProviderUploadID: upload.ProviderUploadID, ResumeFingerprint: strings.Repeat("a", 64),
		PartSize: 32 << 20, PartCount: int((fileSize + (32 << 20) - 1) / (32 << 20)), Status: status, ExpiresAt: expiresAt,
		CreatedAt: cleanupTestNow.Add(-time.Hour), UpdatedAt: cleanupTestNow.Add(-time.Hour),
	}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	return file, session
}

func assertCleanupTerminalState(
	t *testing.T,
	db *gorm.DB,
	fileID string,
	sessionID string,
	fileStatus string,
	sessionStatus domain.FileUploadSessionStatus,
) {
	t.Helper()
	var file domain.FileObject
	if err := db.First(&file, "id = ?", fileID).Error; err != nil {
		t.Fatal(err)
	}
	var session domain.FileUploadSession
	if err := db.First(&session, "id = ?", sessionID).Error; err != nil {
		t.Fatal(err)
	}
	if file.Status != fileStatus || session.Status != sessionStatus || file.DeletedAt == nil {
		t.Fatalf("file/session = %#v / %#v", file, session)
	}
}

func assertCleanupAudits(t *testing.T, db *gorm.DB, sessionID string, want int64) {
	t.Helper()
	var audits []domain.AuditLog
	if err := db.Where("resource = ? AND resource_id = ?", "file-upload-session", sessionID).Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if int64(len(audits)) != want {
		t.Fatalf("audit count = %d, want %d", len(audits), want)
	}
	raw, err := json.Marshal(audits)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "providerUploadId") || strings.Contains(string(raw), "resumeFingerprint") || strings.Contains(string(raw), "etag") {
		t.Fatalf("sensitive upload state leaked into audit: %s", raw)
	}
}

func applyMultipartCleanupFixture(db *gorm.DB) error {
	return db.Exec(`
		CREATE TABLE file_objects (
			id TEXT PRIMARY KEY, storage_profile_id TEXT, provider TEXT NOT NULL,
			bucket TEXT NOT NULL DEFAULT '', object_key TEXT NOT NULL UNIQUE,
			original_name TEXT NOT NULL, content_type TEXT NOT NULL, size INTEGER NOT NULL,
			etag TEXT NOT NULL DEFAULT '', sha256 TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0,
			owner_id TEXT NOT NULL REFERENCES users(id), visibility TEXT NOT NULL,
			status TEXT NOT NULL, upload_expires_at DATETIME, created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL, deleted_at DATETIME
		);
		CREATE TABLE file_upload_sessions (
			id TEXT PRIMARY KEY, file_id TEXT NOT NULL UNIQUE REFERENCES file_objects(id),
			provider_upload_id TEXT NOT NULL, resume_fingerprint TEXT NOT NULL,
			part_size INTEGER NOT NULL, part_count INTEGER NOT NULL,
			status TEXT NOT NULL, expires_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
		);
		CREATE INDEX idx_file_upload_sessions_status_expires
			ON file_upload_sessions(status, expires_at);
		CREATE TABLE file_upload_parts (
			session_id TEXT NOT NULL REFERENCES file_upload_sessions(id) ON DELETE CASCADE,
			part_number INTEGER NOT NULL, size INTEGER NOT NULL, etag TEXT NOT NULL,
			confirmed_at DATETIME NOT NULL, PRIMARY KEY (session_id, part_number)
		);
	`).Error
}
