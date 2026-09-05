package multipartcleanup

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	frameworkstorage "github.com/xgtian-root/aginex/framework/storage"
	"github.com/xgtian-root/aginex/internal/domain"
	platformstorage "github.com/xgtian-root/aginex/internal/platform/storage"
	"gorm.io/gorm"
)

func TestRecoverCompletingUsesPersistedManifestAndVerifiesFinalObject(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	file, session := seedUploadSessionSized(
		t,
		db,
		registry,
		profileID,
		domain.FileUploadSessionStatusCompleting,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
		recoveryPartSize+7,
	)
	makeRecoveryStale(t, db, session.ID)
	multipart, err := registry.ResolveMultipart(profileID)
	if err != nil {
		t.Fatal(err)
	}
	upload := frameworkstorage.MultipartUpload{
		Key: file.ObjectKey, ProviderUploadID: session.ProviderUploadID,
	}
	localStore, ok := platformstorage.AsLocal(mustResolveStore(t, registry, profileID))
	if !ok {
		t.Fatal("cleanup test storage does not expose local multipart writes")
	}
	first, err := localStore.PutMultipartPart(context.Background(), frameworkstorage.MultipartPartRequest{
		Upload: upload, PartNumber: 1, Size: recoveryPartSize,
	}, io.LimitReader(zeroReader{}, recoveryPartSize))
	if err != nil {
		t.Fatal(err)
	}
	second, err := localStore.PutMultipartPart(context.Background(), frameworkstorage.MultipartPartRequest{
		Upload: upload, PartNumber: 2, Size: 7,
	}, io.LimitReader(zeroReader{}, 7))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create([]domain.FileUploadPart{
		{SessionID: session.ID, PartNumber: 1, Size: first.Size, ETag: first.ETag, ConfirmedAt: cleanupTestNow.Add(-2 * time.Hour)},
		{SessionID: session.ID, PartNumber: 2, Size: second.Size, ETag: second.ETag, ConfirmedAt: cleanupTestNow.Add(-2 * time.Hour)},
	}).Error; err != nil {
		t.Fatal(err)
	}

	handler := cleanupTestHandler(t, db, registry)
	if err := handler.Recover(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}

	var storedFile domain.FileObject
	if err := db.First(&storedFile, "id = ?", file.ID).Error; err != nil {
		t.Fatal(err)
	}
	var storedSession domain.FileUploadSession
	if err := db.First(&storedSession, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedFile.Status != "ready" || storedSession.Status != domain.FileUploadSessionStatusCompleted {
		t.Fatalf("recovered file/session = %#v / %#v", storedFile, storedSession)
	}
	if storedFile.Size != recoveryPartSize+7 || len(storedFile.SHA256) != 64 || storedFile.ContentType != frameworkstorage.StoredContentType {
		t.Fatalf("verified metadata = %#v", storedFile)
	}
	if _, err := multipart.ListUploadedParts(context.Background(), upload); !errors.Is(err, platformstorage.ErrMultipartNotFound) {
		t.Fatalf("completed provider upload still has multipart inventory: %v", err)
	}
	assertCleanupAudits(t, db, session.ID, 3)

	// Simulate a process that received a successful provider Complete response
	// but lost both subsequent database writes. Recovery must trust the exact
	// final object after full verification and must not require the now-removed
	// multipart inventory.
	staleAt := cleanupTestNow.Add(-CompletionRecoveryGrace - time.Minute)
	if err := db.Model(&domain.FileUploadSession{}).Where("id = ?", session.ID).UpdateColumns(map[string]any{
		"status": domain.FileUploadSessionStatusCompleting, "updated_at": staleAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.FileObject{}).Where("id = ?", file.ID).UpdateColumns(map[string]any{
		"status": "pending", "content_type": frameworkstorage.StoredContentType,
		"sha256": "", "updated_at": staleAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := handler.Recover(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&storedFile, "id = ?", file.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&storedSession, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if storedFile.Status != "ready" || storedSession.Status != domain.FileUploadSessionStatusCompleted || len(storedFile.SHA256) != 64 {
		t.Fatalf("recovered already-merged file/session = %#v / %#v", storedFile, storedSession)
	}
	assertCleanupAudits(t, db, session.ID, 6)
}

func TestRecoverCompletingMismatchReopensAndNeverAborts(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	file, session := seedUploadSessionSized(
		t,
		db,
		registry,
		profileID,
		domain.FileUploadSessionStatusCompleting,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
		recoveryPartSize+11,
	)
	makeRecoveryStale(t, db, session.ID)
	if err := db.Create([]domain.FileUploadPart{
		{SessionID: session.ID, PartNumber: 1, Size: recoveryPartSize, ETag: "stale-one", ConfirmedAt: cleanupTestNow.Add(-2 * time.Hour)},
		{SessionID: session.ID, PartNumber: 2, Size: 11, ETag: "stale-two", ConfirmedAt: cleanupTestNow.Add(-2 * time.Hour)},
	}).Error; err != nil {
		t.Fatal(err)
	}

	handler := cleanupTestHandler(t, db, registry)
	if err := handler.Recover(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	var stored domain.FileUploadSession
	if err := db.First(&stored, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.FileUploadSessionStatusActive {
		t.Fatalf("session status = %q, want active", stored.Status)
	}
	var partCount int64
	if err := db.Model(&domain.FileUploadPart{}).Where("session_id = ?", session.ID).Count(&partCount).Error; err != nil {
		t.Fatal(err)
	}
	if partCount != 0 {
		t.Fatalf("stale confirmed parts = %d, want 0", partCount)
	}
	multipart, err := registry.ResolveMultipart(profileID)
	if err != nil {
		t.Fatal(err)
	}
	// A successful empty inventory proves recovery did not call AbortMultipart.
	parts, err := multipart.ListUploadedParts(context.Background(), frameworkstorage.MultipartUpload{
		Key: file.ObjectKey, ProviderUploadID: session.ProviderUploadID,
	})
	if err != nil || len(parts) != 0 {
		t.Fatalf("provider upload was not preserved: parts=%#v err=%v", parts, err)
	}
	assertCleanupAudits(t, db, session.ID, 2)
}

func TestRecoveryLeaseRequiresSafeWindowAndCannotBeReclaimed(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	_, session := seedUploadSession(
		t,
		db,
		registry,
		profileID,
		domain.FileUploadSessionStatusCompleting,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
	)
	handler := cleanupTestHandler(t, db, registry)
	if err := handler.Recover(context.Background(), session.ID); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("fresh recovery error = %v, want ErrUnsafeState", err)
	}
	makeRecoveryStale(t, db, session.ID)
	if _, _, err := handler.acquireRecoveryLease(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := handler.acquireRecoveryLease(context.Background(), session.ID); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("reclaimed lease error = %v, want ErrUnsafeState", err)
	}
	assertCleanupAudits(t, db, session.ID, 1)
}

func TestRecoveryLeaseRollsBackWhenAuditCannotCommit(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	file, session := seedUploadSession(
		t,
		db,
		registry,
		profileID,
		domain.FileUploadSessionStatusCompleting,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
	)
	makeRecoveryStale(t, db, session.ID)
	var before domain.FileUploadSession
	if err := db.First(&before, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	handler := cleanupTestHandler(t, db, registry)
	if err := handler.Recover(context.Background(), session.ID); err == nil {
		t.Fatal("recovery unexpectedly claimed an unaudited lease")
	}
	var after domain.FileUploadSession
	if err := db.First(&after, "id = ?", session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("unaudited lease persisted: before=%#v after=%#v", before, after)
	}
	multipart, err := registry.ResolveMultipart(profileID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := multipart.ListUploadedParts(context.Background(), frameworkstorage.MultipartUpload{
		Key: file.ObjectKey, ProviderUploadID: session.ProviderUploadID,
	}); err != nil {
		t.Fatalf("provider upload changed before an audited lease: %v", err)
	}
}

func TestMarkRecoveryVerifyingLetsFinalObjectWinConcurrentActiveReset(t *testing.T) {
	db, registry, profileID := cleanupTestRuntime(t)
	_, session := seedUploadSession(
		t,
		db,
		registry,
		profileID,
		domain.FileUploadSessionStatusCompleting,
		cleanupTestNow.Add(time.Hour),
		"pending",
		true,
	)
	makeRecoveryStale(t, db, session.ID)
	handler := cleanupTestHandler(t, db, registry)
	file, leased, err := handler.acquireRecoveryLease(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.FileUploadSession{}).Where("id = ?", session.ID).UpdateColumns(map[string]any{
		"status": domain.FileUploadSessionStatusActive, "updated_at": cleanupTestNow.Add(time.Second),
	}).Error; err != nil {
		t.Fatal(err)
	}

	_, verifying, err := handler.markRecoveryVerifying(context.Background(), file, leased)
	if err != nil {
		t.Fatal(err)
	}
	if verifying.Status != domain.FileUploadSessionStatusVerifying {
		t.Fatalf("session status = %q, want verifying", verifying.Status)
	}
	assertCleanupAudits(t, db, session.ID, 2)
}

func TestRecoveryInventoryTreatsQuotedETagsAsTheSameOpaqueValue(t *testing.T) {
	file := domain.FileObject{Size: recoveryPartSize + 5}
	session := domain.FileUploadSession{PartSize: recoveryPartSize, PartCount: 2}
	persisted := []domain.FileUploadPart{
		{PartNumber: 1, Size: recoveryPartSize, ETag: `"part-one"`},
		{PartNumber: 2, Size: 5, ETag: `part-two`},
	}
	inventory := []frameworkstorage.UploadedPart{
		{PartNumber: 1, Size: recoveryPartSize, ETag: `part-one`},
		{PartNumber: 2, Size: 5, ETag: `W/ "part-two"`},
	}
	matching, stale := recoveryInventoryMatches(file, session, persisted, inventory)
	if !matching || len(stale) != 0 {
		t.Fatalf("matching=%t stale=%v", matching, stale)
	}
}

func makeRecoveryStale(t *testing.T, db *gorm.DB, sessionID string) {
	t.Helper()
	if err := db.Model(&domain.FileUploadSession{}).
		Where("id = ?", sessionID).
		Update("updated_at", cleanupTestNow.Add(-CompletionRecoveryGrace-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
}

func mustResolveStore(t *testing.T, registry *platformstorage.Registry, profileID string) platformstorage.Storage {
	t.Helper()
	store, err := registry.Resolve(profileID)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
