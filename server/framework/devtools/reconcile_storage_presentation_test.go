package devtools

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	platformstorage "github.com/xgtian-root/aginex/server/internal/platform/storage"
	"gorm.io/gorm"
)

func TestReconcileStoragePresentationsIsAuditedAndRerunnable(t *testing.T) {
	databaseConfig := config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "reconcile.db"),
	}
	db, err := database.OpenContext(t.Context(), databaseConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := createReconcileTestSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO file_objects
		(id, storage_profile_id, provider, bucket, object_key, content_type, size, etag, status, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"file-1", "profile-1", "oss", "private-bucket", "uploads/verified.png",
		frameworkstorage.MIMEPNG, 128, "verified-etag", "ready", time.Now().UTC(),
	).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()

	store := &reconcileTestStore{info: frameworkstorage.ObjectInfo{
		Key: "uploads/verified.png", Size: 128,
		ContentType:        frameworkstorage.StoredContentType,
		ContentDisposition: "attachment", ETag: "verified-etag",
	}}
	dependencies := reconcileStorageDependencies{
		loadConfig: func() (config.Config, error) {
			return config.Config{Database: databaseConfig}, nil
		},
		openDB: database.OpenContext,
		registry: func(context.Context, config.Config) (reconcileStorageResolver, error) {
			return reconcileTestResolver{store: store}, nil
		},
		now: func() time.Time { return time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC) },
	}
	var output bytes.Buffer
	if err := reconcileStoragePresentations(t.Context(), &output, dependencies); err != nil {
		t.Fatal(err)
	}
	if store.finalizations != 1 || !strings.Contains(output.String(), "Reconciled 1") {
		t.Fatalf("finalizations/output = %d/%q", store.finalizations, output.String())
	}

	assertReconciledDatabase(t, databaseConfig, "final-etag", 1)
	output.Reset()
	if err := reconcileStoragePresentations(t.Context(), &output, dependencies); err != nil {
		t.Fatal(err)
	}
	if store.finalizations != 1 || !strings.Contains(output.String(), "1 already current") {
		t.Fatalf("second finalizations/output = %d/%q", store.finalizations, output.String())
	}
	assertReconciledDatabase(t, databaseConfig, "final-etag", 1)
}

func createReconcileTestSchema(db *gorm.DB) error {
	for _, statement := range []string{
		`CREATE TABLE file_objects (
			id TEXT PRIMARY KEY, storage_profile_id TEXT, provider TEXT, bucket TEXT,
			object_key TEXT, content_type TEXT, size INTEGER, etag TEXT, status TEXT,
			updated_at DATETIME
		)`,
		`CREATE TABLE audit_logs (
			id TEXT PRIMARY KEY, actor_id TEXT, actor_kind TEXT, action TEXT,
			resource TEXT, resource_id TEXT, result TEXT, source TEXT, summary TEXT,
			request_id TEXT, ip_address TEXT, sanitized_before TEXT, sanitized_after TEXT, created_at DATETIME
		)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func assertReconciledDatabase(
	t *testing.T,
	cfg config.Database,
	wantETag string,
	wantAudits int64,
) {
	t.Helper()
	db, err := database.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	}()
	var etag string
	if err := db.Raw("SELECT etag FROM file_objects WHERE id = ?", "file-1").Scan(&etag).Error; err != nil {
		t.Fatal(err)
	}
	if etag != wantETag {
		t.Fatalf("database ETag = %q, want %q", etag, wantETag)
	}
	var audits int64
	if err := db.Raw("SELECT COUNT(*) FROM audit_logs WHERE action = ?", "files:reconcile-presentation").Scan(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != wantAudits {
		t.Fatalf("audit count = %d, want %d", audits, wantAudits)
	}
}

type reconcileTestResolver struct {
	store platformstorage.Storage
}

func (resolver reconcileTestResolver) Resolve(string) (platformstorage.Storage, error) {
	return resolver.store, nil
}

func (resolver reconcileTestResolver) ResolveLegacy(string, string) (string, platformstorage.Storage, error) {
	return "profile-1", resolver.store, nil
}

type reconcileTestStore struct {
	info          frameworkstorage.ObjectInfo
	finalizations int
}

func (*reconcileTestStore) CreateUpload(context.Context, frameworkstorage.UploadRequest) (frameworkstorage.SignedRequest, error) {
	return frameworkstorage.SignedRequest{}, nil
}

func (*reconcileTestStore) SignRead(context.Context, string, time.Duration) (frameworkstorage.SignedRequest, error) {
	return frameworkstorage.SignedRequest{}, nil
}

func (store *reconcileTestStore) Stat(context.Context, string) (frameworkstorage.ObjectInfo, error) {
	return store.info, nil
}

func (*reconcileTestStore) Open(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (*reconcileTestStore) Delete(context.Context, string) error { return nil }

func (store *reconcileTestStore) FinalizeVerifiedPresentation(
	_ context.Context,
	request frameworkstorage.VerifiedPresentationRequest,
) (frameworkstorage.ObjectInfo, error) {
	store.finalizations++
	store.info.ContentType = request.ContentType
	store.info.ContentDisposition = ""
	store.info.ETag = "final-etag"
	return store.info, nil
}
