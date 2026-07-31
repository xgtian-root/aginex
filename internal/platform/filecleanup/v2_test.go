package filecleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/jobs"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/internal/domain"
)

func TestHandleV2ExpiresPendingUploadAndAuditsOnce(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "pending")
	handler := newCleanupHandler(t, db, store)
	dispatcher := newCleanupV2Dispatcher(t, handler)
	job := cleanupJobV2(
		t,
		file,
		ModePendingExpiry,
		authz.NewUserActor(file.OwnerID),
		"request-upload-intent",
	)

	if err := dispatcher.Dispatch(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(context.Background(), file.ObjectKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat expired object error = %v, want os.ErrNotExist", err)
	}
	var stored domain.FileObject
	if err := db.First(&stored, "id = ?", file.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != "deleted" || stored.DeletedAt == nil ||
		!stored.DeletedAt.Equal(fixedCleanupTime) ||
		!stored.UpdatedAt.Equal(fixedCleanupTime) {
		t.Fatalf("stored file after expiry = %#v", stored)
	}

	var entries []domain.AuditLog
	if err := db.Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("audit count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.ActorID == nil || *entry.ActorID != file.OwnerID ||
		entry.ActorKind != "user" ||
		entry.Action != "files:expire-upload" ||
		entry.Resource != "file" ||
		entry.ResourceID != file.ID ||
		entry.Result != "success" ||
		entry.Source != "worker" ||
		entry.RequestID != "request-upload-intent" {
		t.Fatalf("audit entry = %#v", entry)
	}
	if entry.Before["status"] != "pending" || entry.After["status"] != "deleted" {
		t.Fatalf("audit transition before=%#v after=%#v", entry.Before, entry.After)
	}

	if err := dispatcher.Dispatch(context.Background(), job); err != nil {
		t.Fatalf("repeat expiry delivery error = %v", err)
	}
	assertAuditCount(t, db, 1)
}

func TestHandleV2PendingExpiryTreatsMissingObjectAsIdempotentDelete(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "pending")
	if err := store.Delete(context.Background(), file.ObjectKey); err != nil {
		t.Fatal(err)
	}
	handler := newCleanupHandler(t, db, store)

	if err := handler.HandleV2(
		context.Background(),
		cleanupPayloadV2(t, file, ModePendingExpiry),
	); err != nil {
		t.Fatal(err)
	}
	assertCleanupState(t, db, file.ID, "deleted")
	assertAuditCount(t, db, 1)
}

func TestHandleV2PendingExpiryNoOpsAfterIntentLeavesPendingState(t *testing.T) {
	for _, status := range []string{"ready", "invalid", "deleting", "delete_failed", "deleted"} {
		t.Run(status, func(t *testing.T) {
			db := openCleanupDatabase(t)
			store := newCleanupStore(t)
			file := seedCleanupFile(t, db, store, "pending")
			if err := db.Model(&domain.FileObject{}).
				Where("id = ?", file.ID).
				Update("status", status).Error; err != nil {
				t.Fatal(err)
			}
			handler := newCleanupHandler(t, db, store)

			if err := handler.HandleV2(
				context.Background(),
				cleanupPayloadV2(t, file, ModePendingExpiry),
			); err != nil {
				t.Fatalf("stale expiry delivery for %q returned %v", status, err)
			}
			assertCleanupState(t, db, file.ID, status)
			if _, err := store.Stat(context.Background(), file.ObjectKey); err != nil {
				t.Fatalf("stale expiry deleted %q object: %v", status, err)
			}
			assertAuditCount(t, db, 0)
		})
	}
}

func TestHandleV2ExplicitDeleteRetainsVersionOneSemantics(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "deleting")
	handler := newCleanupHandler(t, db, store)

	if err := handler.HandleV2(
		context.Background(),
		cleanupPayloadV2(t, file, ModeExplicitDelete),
	); err != nil {
		t.Fatal(err)
	}
	assertCleanupState(t, db, file.ID, "deleted")
	var entry domain.AuditLog
	if err := db.First(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if entry.Action != "files:delete-complete" {
		t.Fatalf("audit action = %q, want files:delete-complete", entry.Action)
	}
}

func TestHandleV2PendingExpiryRollsBackMetadataWhenAuditFails(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "pending")
	handler := newCleanupHandler(t, db, store)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	if err := handler.HandleV2(
		context.Background(),
		cleanupPayloadV2(t, file, ModePendingExpiry),
	); err == nil {
		t.Fatal("pending expiry unexpectedly succeeded without the audit table")
	}
	assertCleanupState(t, db, file.ID, "pending")
	if _, err := store.Stat(context.Background(), file.ObjectKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat physically deleted object error = %v, want os.ErrNotExist", err)
	}
}

func TestHandleV2ValidatesModeAndExactObjectIdentity(t *testing.T) {
	db := openCleanupDatabase(t)
	store := newCleanupStore(t)
	file := seedCleanupFile(t, db, store, "pending")
	handler := newCleanupHandler(t, db, store)

	cases := []struct {
		name    string
		raw     json.RawMessage
		wantErr error
	}{
		{
			name: "unknown mode",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":%q,"mode":"expiry"}`,
				file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "missing mode",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":%q}`,
				file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "unknown field",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":%q,"mode":"pending-expiry","bucket":"private"}`,
				file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "noncanonical file id",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":%q,"mode":"pending-expiry"}`,
				"urn:uuid:"+file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "provider whitespace",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":" local","objectKey":%q,"mode":"pending-expiry"}`,
				file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "unsafe object key",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":"../image.png","mode":"pending-expiry"}`,
				file.ID,
			)),
			wantErr: ErrInvalidPayload,
		},
		{
			name: "different configured provider",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"s3","objectKey":%q,"mode":"pending-expiry"}`,
				file.ID,
				file.ObjectKey,
			)),
			wantErr: ErrObjectChanged,
		},
		{
			name: "different stored object key",
			raw: json.RawMessage(fmt.Sprintf(
				`{"fileId":%q,"provider":"local","objectKey":"files/other.png","mode":"pending-expiry"}`,
				file.ID,
			)),
			wantErr: ErrObjectChanged,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			err := handler.HandleV2(context.Background(), test.raw)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("cleanup error = %v, want %v", err, test.wantErr)
			}
		})
	}
	assertCleanupState(t, db, file.ID, "pending")
	if _, err := store.Stat(context.Background(), file.ObjectKey); err != nil {
		t.Fatalf("validated object was changed: %v", err)
	}
	assertAuditCount(t, db, 0)
}

func newCleanupV2Dispatcher(t *testing.T, handler *Handler) *jobs.Dispatcher {
	t.Helper()
	registry := module.NewRegistry()
	if err := registry.RegisterJobHandler(module.JobHandlerDefinition{
		Type:    JobType,
		Version: PayloadVersion2,
		Handle:  handler.HandleV2,
	}); err != nil {
		t.Fatal(err)
	}
	dispatcher, err := jobs.NewDispatcher(registry)
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher
}

func cleanupJobV2(
	t *testing.T,
	file domain.FileObject,
	mode Mode,
	actor authz.Actor,
	requestID string,
) jobs.Job {
	t.Helper()
	return jobs.Job{
		ID:          uuid.NewString(),
		Type:        JobType,
		Version:     PayloadVersion2,
		Payload:     cleanupPayloadV2(t, file, mode),
		State:       jobs.StateRunning,
		Attempts:    1,
		MaxAttempts: 5,
		LockedBy:    "worker-test",
		CreatedBy:   actor,
		Trace: jobs.TraceContext{
			RequestID: requestID,
		},
	}
}

func cleanupPayloadV2(t *testing.T, file domain.FileObject, mode Mode) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(PayloadV2{
		FileID:    file.ID,
		Provider:  file.Provider,
		ObjectKey: file.ObjectKey,
		Mode:      mode,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
