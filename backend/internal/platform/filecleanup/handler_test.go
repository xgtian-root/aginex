package filecleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/jobs"
	"github.com/xgtian-root/aginex/backend/framework/module"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/database"
	"github.com/xgtian-root/aginex/backend/internal/platform/migrate"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
)

var fixedCleanupTime = time.Date(2026, 7, 31, 12, 30, 0, 0, time.UTC)

func TestHandlerDeletesObjectAndAtomicallyAuditsExecutionContext(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "deleting")
	handler := newCleanupHandler(t, db, store)
	dispatcher := newCleanupDispatcher(t, handler)

	job := cleanupJob(t, file, authz.NewUserActor(file.OwnerID), "request-file-delete")
	if err := dispatcher.Dispatch(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Stat(context.Background(), file.ObjectKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat deleted object error = %v, want os.ErrNotExist", err)
	}

	var stored domain.FileObject
	if err := db.First(&stored, "id = ?", file.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "deleted" || stored.DeletedAt == nil ||
		!stored.DeletedAt.Equal(fixedCleanupTime) ||
		!stored.UpdatedAt.Equal(fixedCleanupTime) {
		t.Fatalf("stored file after cleanup = %#v", stored)
	}

	var entries []domain.AuditLog
	if err := db.Order("created_at ASC").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.ActorID == nil || *entry.ActorID != file.OwnerID ||
		entry.ActorKind != "user" ||
		entry.Action != "files:delete-complete" ||
		entry.Resource != "file" ||
		entry.ResourceID != file.ID ||
		entry.Result != "success" ||
		entry.Source != "worker" ||
		entry.RequestID != "request-file-delete" {
		t.Fatalf("audit entry = %#v", entry)
	}
	if entry.Before["status"] != "deleting" || entry.After["status"] != "deleted" {
		t.Fatalf("audit transition before=%#v after=%#v", entry.Before, entry.After)
	}
	if _, ok := entry.After["deletedAt"]; !ok {
		t.Fatalf("audit after fields = %#v, want deletedAt", entry.After)
	}

	if err := dispatcher.Dispatch(context.Background(), job); err != nil {
		t.Fatalf("repeat delivery error = %v", err)
	}
	mismatched := job
	mismatched.ID = uuid.NewString()
	mismatched.Payload = json.RawMessage(fmt.Sprintf(
		`{"fileId":%q,"provider":"local","objectKey":"files/other.png"}`,
		file.ID,
	))
	if err := dispatcher.Dispatch(context.Background(), mismatched); !errors.Is(err, ErrObjectChanged) {
		t.Fatalf("deleted-file metadata mismatch error = %v, want ErrObjectChanged", err)
	}
	var count int64
	if err := db.Model(&domain.AuditLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("audit count after repeat = %d, want 1", count)
	}
}

func TestHandlerStorageFailureLeavesMetadataAndAuditUntouched(t *testing.T) {
	db := openCleanupDatabase(t)
	local := newCleanupStore(t)
	file := seedCleanupFile(t, db, local, "deleting")
	deleteFailure := errors.New("storage unavailable")
	store := deleteOverrideStorage{
		Storage: local,
		delete: func(context.Context, string) error {
			return deleteFailure
		},
	}
	handler := newCleanupHandler(t, db, store)

	err := handler.Handle(context.Background(), cleanupPayload(t, file))
	if !errors.Is(err, deleteFailure) {
		t.Fatalf("cleanup error = %v, want storage failure", err)
	}
	assertCleanupState(t, db, file.ID, "deleting")
	if _, err := local.Stat(context.Background(), file.ObjectKey); err != nil {
		t.Fatalf("object was changed after failed delete: %v", err)
	}
	assertAuditCount(t, db, 0)
}

func TestHandlerRollsBackMetadataWhenAuditWriteFails(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "deleting")
	handler := newCleanupHandler(t, db, store)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	if err := handler.Handle(context.Background(), cleanupPayload(t, file)); err == nil {
		t.Fatal("cleanup unexpectedly succeeded without the audit table")
	}
	assertCleanupState(t, db, file.ID, "deleting")
	if _, err := store.Stat(context.Background(), file.ObjectKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat physically deleted object error = %v, want os.ErrNotExist", err)
	}
}

func TestHandlerRejectsFilesOutsideCleanupStates(t *testing.T) {
	for _, status := range []string{"pending", "ready", "uploading"} {
		t.Run(status, func(t *testing.T) {
			db := openCleanupDatabase(t)
			store := newCleanupStore(t)
			file := seedCleanupFile(t, db, store, status)
			handler := newCleanupHandler(t, db, store)

			err := handler.Handle(context.Background(), cleanupPayload(t, file))
			if !errors.Is(err, ErrUnsafeState) {
				t.Fatalf("cleanup error = %v, want ErrUnsafeState", err)
			}
			assertCleanupState(t, db, file.ID, status)
			if _, err := store.Stat(context.Background(), file.ObjectKey); err != nil {
				t.Fatalf("object was changed for status %q: %v", status, err)
			}
			assertAuditCount(t, db, 0)
		})
	}
}

func TestHandlerValidatesPayloadAgainstStoredObjectMetadata(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "deleting")
	handler := newCleanupHandler(t, db, store)

	cases := []struct {
		name    string
		raw     json.RawMessage
		wantErr error
	}{
		{
			name: "unknown field",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":%q,"bucket":"private"}`,
				file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name:    "invalid file id",
			raw:     json.RawMessage(`{"fileId":"file-1","provider":"local","objectKey":"files/image.png"}`),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "noncanonical file id",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":%q}`,
				"urn:uuid:"+file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "unsafe object key",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":"../image.png"}`,
				file.ID,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "different configured provider",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"s3","objectKey":%q}`,
				file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrObjectChanged,
		},
		{
			name: "different stored object key",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":"files/other.png"}`,
				file.ID,
			)),
			wantErr: ErrObjectChanged,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := handler.Handle(context.Background(), test.raw)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("cleanup error = %v, want %v", err, test.wantErr)
			}
		})
	}

	assertCleanupState(t, db, file.ID, "deleting")
	if _, err := store.Stat(context.Background(), file.ObjectKey); err != nil {
		t.Fatalf("validated object was changed: %v", err)
	}
	assertAuditCount(t, db, 0)
}

func TestHandlerTreatsConcurrentCompletionAsIdempotent(t *testing.T) {
	db := openCleanupDatabase(t)
	local := newCleanupStore(t)
	file := seedCleanupFile(t, db, local, "deleting")
	competingHandler := newCleanupHandler(t, db, local)
	store := deleteOverrideStorage{
		Storage: local,
		delete: func(ctx context.Context, key string) error {
			if err := local.Delete(ctx, key); err != nil {
				return err
			}
			return competingHandler.Handle(ctx, cleanupPayload(t, file))
		},
	}
	handler := newCleanupHandler(t, db, store)

	if err := handler.Handle(context.Background(), cleanupPayload(t, file)); err != nil {
		t.Fatalf("concurrent completion error = %v", err)
	}
	assertCleanupState(t, db, file.ID, "deleted")
	assertAuditCount(t, db, 1)
}

func TestHandlerTreatsConcurrentMetadataRemovalAsIdempotent(t *testing.T) {
	db := openCleanupDatabase(t)
	local := newCleanupStore(t)
	file := seedCleanupFile(t, db, local, "deleting")
	store := deleteOverrideStorage{
		Storage: local,
		delete: func(ctx context.Context, key string) error {
			if err := local.Delete(ctx, key); err != nil {
				return err
			}
			return db.WithContext(ctx).Delete(&domain.FileObject{}, "id = ?", file.ID).Error
		},
	}
	handler := newCleanupHandler(t, db, store)

	if err := handler.Handle(context.Background(), cleanupPayload(t, file)); err != nil {
		t.Fatalf("concurrent metadata removal error = %v", err)
	}
	if err := handler.Handle(context.Background(), cleanupPayload(t, file)); err != nil {
		t.Fatalf("repeat orphan cleanup error = %v", err)
	}
	var count int64
	if err := db.Model(&domain.FileObject{}).Where("id = ?", file.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("file row count = %d, want 0", count)
	}
	assertAuditCount(t, db, 0)
}

type deleteOverrideStorage struct {
	storage.Storage
	delete func(context.Context, string) error
}

func (store deleteOverrideStorage) Delete(ctx context.Context, key string) error {
	return store.delete(ctx, key)
}

func openCleanupDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open(config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "file-cleanup.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	if err := migrate.Up(sqlDB, "sqlite"); err != nil {
		t.Fatal(err)
	}
	if err := applyFileObjectModuleFixture(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// applyFileObjectModuleFixture declares the file-cleanup package's opt-in
// dependency without importing internal/app, which would create an import
// cycle because the application composition owns this handler. Production
// schema changes continue to use the FilesModule Goose migration; this
// test-only fixture mirrors that module's final SQLite table shape.
func applyFileObjectModuleFixture(db *gorm.DB) error {
	return db.Exec(`
		CREATE TABLE file_objects (
			id TEXT PRIMARY KEY,
			storage_profile_id TEXT,
			provider TEXT NOT NULL,
			bucket TEXT NOT NULL DEFAULT '',
			object_key TEXT NOT NULL UNIQUE,
			original_name TEXT NOT NULL,
			content_type TEXT NOT NULL,
			size INTEGER NOT NULL,
			etag TEXT NOT NULL DEFAULT '',
			sha256 TEXT NOT NULL DEFAULT '',
			width INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
			height INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
			owner_id TEXT NOT NULL REFERENCES users(id),
			visibility TEXT NOT NULL DEFAULT 'private',
			status TEXT NOT NULL DEFAULT 'pending',
			upload_expires_at DATETIME,
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL,
			deleted_at DATETIME
		);
		CREATE INDEX idx_file_objects_owner_id ON file_objects(owner_id);
		CREATE INDEX idx_file_objects_storage_profile_id ON file_objects(storage_profile_id);
		CREATE INDEX idx_file_objects_status ON file_objects(status);
		CREATE INDEX idx_file_objects_deleted_at ON file_objects(deleted_at);
	`).Error
}

func newCleanupStore(t *testing.T) *storage.Local {
	t.Helper()
	store, err := storage.NewLocal(
		t.TempDir(),
		"/api/v1/files/upload",
		"/api/v1/files",
		storage.DefaultImagePolicy(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func seedCleanupFile(
	t *testing.T,
	db *gorm.DB,
	store *storage.Local,
	status string,
) domain.FileObject {
	t.Helper()
	now := fixedCleanupTime.Add(-time.Hour)
	user := domain.User{
		ID:          uuid.NewString(),
		Email:       uuid.NewString() + "@example.com",
		DisplayName: "File owner",
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	file := domain.FileObject{
		ID:           uuid.NewString(),
		Provider:     "local",
		Bucket:       "",
		ObjectKey:    "files/" + uuid.NewString() + ".png",
		OriginalName: "stamp.png",
		ContentType:  "image/png",
		Size:         16,
		ETag:         "etag",
		SHA256:       "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Width:        1,
		Height:       1,
		OwnerID:      user.ID,
		Visibility:   "private",
		Status:       status,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	body := []byte("\x89PNG\r\n\x1a\nfixture")
	file.Size = int64(len(body))
	if _, err := store.Put(context.Background(), file.ObjectKey, bytes.NewReader(body), int64(len(body))); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	return file
}

func newCleanupHandler(
	t *testing.T,
	db *gorm.DB,
	store storage.Storage,
) *Handler {
	t.Helper()
	handler, err := New(db, store, Config{
		Provider:      "local",
		SystemActorID: "cleanup-worker",
		Clock: func() time.Time {
			return fixedCleanupTime
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func newCleanupDispatcher(t *testing.T, handler *Handler) *jobs.Dispatcher {
	t.Helper()
	registry := module.NewRegistry()
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    "storage.cleanup",
		Version: 1,
		Handle:  handler.Handle,
	}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := jobs.NewDispatcher(registry)
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func cleanupJob(
	t *testing.T,
	file domain.FileObject,
	actor authz.Actor,
	requestID string,
) jobs.Job {
	t.Helper()
	return jobs.Job{
		ID:          uuid.NewString(),
		Type:        "storage.cleanup",
		Version:     1,
		Payload:     cleanupPayload(t, file),
		State:       jobs.StateRunning,
		Attempts:    1,
		MaxAttempts: 5,
		LockedBy:    "worker-test",
		CreatedBy:   actor,
		Trace: jobs.TraceContext{
			RequestID:   requestID,
			TraceParent: "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
		},
	}
}

func cleanupPayload(t *testing.T, file domain.FileObject) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(payload{
		FileID:    file.ID,
		Provider:  file.Provider,
		ObjectKey: file.ObjectKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertCleanupState(t *testing.T, db *gorm.DB, id, status string) {
	t.Helper()
	var file domain.FileObject
	if err := db.First(&file, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if file.Status != status {
		t.Fatalf("file status = %q, want %q", file.Status, status)
	}
}

func assertAuditCount(t *testing.T, db *gorm.DB, expected int64) {
	t.Helper()
	var count int64
	if err := db.Model(&domain.AuditLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("audit count = %d, want %d", count, expected)
	}
}
