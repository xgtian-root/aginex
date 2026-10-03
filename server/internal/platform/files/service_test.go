package files_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/server/framework/audit"
	frameworkfiles "github.com/xgtian-root/aginex/server/framework/files"
	"github.com/xgtian-root/aginex/server/framework/observability"
	"github.com/xgtian-root/aginex/server/framework/uow"
	"github.com/xgtian-root/aginex/server/internal/app"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/auditlog"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"github.com/xgtian-root/aginex/server/internal/platform/filereferences"
	fileservice "github.com/xgtian-root/aginex/server/internal/platform/files"
	"github.com/xgtian-root/aginex/server/internal/platform/migrate"
	"github.com/xgtian-root/aginex/server/internal/platform/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type fixture struct {
	db      *gorm.DB
	service frameworkfiles.Service
	writes  *uow.UnitOfWork
	user    domain.User
	profile string
}

func openFixture(t *testing.T, driver, dsn string) *fixture {
	t.Helper()
	if driver == "sqlite" {
		dsn = filepath.Join(t.TempDir(), "files.db")
	}
	db, err := database.Open(config.Database{Driver: driver, DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := migrate.Up(sqlDB, driver); err != nil {
		t.Fatal(err)
	}
	if err := app.MigrateModulesUp(t.Context(), sqlDB, driver, app.FilesModule()); err != nil {
		t.Fatal(err)
	}
	cfg := config.WithDefaults(config.Config{Environment: "test", Storage: config.Storage{Driver: "local", LocalRoot: t.TempDir()}})
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := storage.NewRegistry(t.Context(), cfg, recorder)
	if err != nil {
		t.Fatal(err)
	}
	service, err := fileservice.New(db, registry)
	if err != nil {
		t.Fatal(err)
	}
	writes, err := uow.New(db, auditlog.Recorder{})
	if err != nil {
		t.Fatal(err)
	}
	user := domain.User{ID: uuid.NewString(), Email: uuid.NewString() + "@example.test", DisplayName: "before", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return &fixture{db: db, service: service, writes: writes, user: user, profile: cfg.StorageRuntime().ActiveProfileID}
}

func (f *fixture) file(t *testing.T, status string) domain.FileObject {
	t.Helper()
	file := domain.FileObject{ID: uuid.NewString(), StorageProfileID: &f.profile, Provider: "local", ObjectKey: "files/" + uuid.NewString(), OriginalName: "example.png", ContentType: "image/png", Size: 4, SHA256: strings.Repeat("a", 64), Width: 1, Height: 1, OwnerID: f.user.ID, Visibility: "private", Status: status}
	if err := f.db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	return file
}

func owner() frameworkfiles.ReferenceOwner {
	return frameworkfiles.ReferenceOwner{Resource: "articles", ResourceID: uuid.NewString()}
}

func event(owner frameworkfiles.ReferenceOwner) audit.Event {
	return audit.Event{ActorKind: audit.ActorSystem, Action: "articles:save", Resource: owner.Resource, ResourceID: owner.ResourceID, Result: audit.ResultSuccess, Source: audit.SourceSystem}
}

func (f *fixture) replace(ctx context.Context, owner frameworkfiles.ReferenceOwner, ids ...string) error {
	return f.writes.Run(ctx, func(tx *gorm.DB) (audit.Event, error) {
		references, err := f.service.Bind(tx)
		if err != nil {
			return audit.Event{}, err
		}
		_, err = references.Replace(ctx, owner, ids)
		return event(owner), err
	})
}

func (f *fixture) referenced(t *testing.T, owner frameworkfiles.ReferenceOwner) []string {
	t.Helper()
	var rows []filereferences.Reference
	if err := f.db.Where("resource = ? AND resource_id = ?", owner.Resource, owner.ResourceID).Order("file_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.FileID)
	}
	return ids
}

func (f *fixture) delete(ctx context.Context, fileID string) error {
	return f.writes.Run(ctx, func(tx *gorm.DB) (audit.Event, error) {
		var file domain.FileObject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, "id = ?", fileID).Error; err != nil {
			return audit.Event{}, err
		}
		if err := filereferences.CheckUnreferenced(ctx, tx, fileID); err != nil {
			return audit.Event{}, err
		}
		err := tx.Model(&file).Update("status", "deleting").Error
		return audit.Event{ActorKind: audit.ActorSystem, Action: "files:delete", Resource: "file", ResourceID: fileID, Result: audit.ResultSuccess, Source: audit.SourceSystem}, err
	})
}

func TestReferencesSharedIdempotentAndExplicitUnbinding(t *testing.T) {
	f := openFixture(t, "sqlite", "")
	file := f.file(t, "ready")
	a, b := owner(), owner()
	for _, item := range []frameworkfiles.ReferenceOwner{a, b, a} {
		if err := f.replace(t.Context(), item, file.ID, file.ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.referenced(t, a); !slices.Equal(got, []string{file.ID}) {
		t.Fatalf("deduplicated set = %v", got)
	}
	for _, item := range []frameworkfiles.ReferenceOwner{a, b} {
		if err := f.delete(t.Context(), file.ID); !errors.Is(err, frameworkfiles.ErrInUse) {
			t.Fatalf("shared deletion error = %v", err)
		}
		if err := f.replace(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.GetReady(t.Context(), file.ID); err != nil {
		t.Fatalf("unbinding deleted file: %v", err)
	}
	if err := f.delete(t.Context(), file.ID); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&filereferences.Owner{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("persistent owner locks count=%d err=%v", count, err)
	}
}

func TestReferencesOnlyAcceptReadyFilesAndValidInputs(t *testing.T) {
	f := openFixture(t, "sqlite", "")
	for _, status := range []string{"pending", "invalid", "deleting", "delete_failed", "deleted"} {
		t.Run(status, func(t *testing.T) {
			file := f.file(t, status)
			if err := f.replace(t.Context(), owner(), file.ID); !errors.Is(err, frameworkfiles.ErrNotReady) {
				t.Fatalf("bind %s error = %v", status, err)
			}
			if _, err := f.service.GetReady(t.Context(), file.ID); !errors.Is(err, frameworkfiles.ErrNotReady) {
				t.Fatalf("read %s error = %v", status, err)
			}
		})
	}
	if err := f.replace(t.Context(), owner(), uuid.NewString()); !errors.Is(err, frameworkfiles.ErrNotFound) {
		t.Fatalf("missing file error = %v", err)
	}
	for _, ids := range [][]string{{""}, {"not-a-uuid"}, make([]string, frameworkfiles.MaxReferences+1)} {
		if err := f.replace(t.Context(), owner(), ids...); !errors.Is(err, frameworkfiles.ErrInvalidArgument) {
			t.Fatalf("invalid IDs error = %v", err)
		}
	}
	for _, value := range []frameworkfiles.ReferenceOwner{{}, {Resource: "Uppercase", ResourceID: "1"}, {Resource: "articles", ResourceID: " 1"}, {Resource: "articles", ResourceID: strings.Repeat("a", 161)}} {
		if err := f.replace(t.Context(), value); !errors.Is(err, frameworkfiles.ErrInvalidArgument) {
			t.Fatalf("invalid owner error = %v", err)
		}
	}
	ready := f.file(t, "ready")
	now := time.Now().UTC()
	if err := f.db.Model(&ready).Update("deleted_at", now).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.replace(t.Context(), owner(), ready.ID); !errors.Is(err, frameworkfiles.ErrNotReady) {
		t.Fatalf("soft deleted ready file error = %v", err)
	}
}

type failingRecorder struct{}

var errAudit = errors.New("test audit unavailable")

func (failingRecorder) Record(context.Context, *gorm.DB, audit.Event) error { return errAudit }

func TestReferencesBusinessAndAuditFailuresRollBackTogether(t *testing.T) {
	for _, target := range []struct{ driver, environment string }{
		{"sqlite", ""},
		{"postgres", "AGINEX_FILES_TEST_POSTGRES_DSN"},
		{"mysql", "AGINEX_FILES_TEST_MYSQL_DSN"},
	} {
		t.Run(target.driver, func(t *testing.T) {
			dsn := os.Getenv(target.environment)
			if target.environment != "" && dsn == "" {
				t.Skip("dedicated file-service integration DSN is not configured")
			}
			testRollback(t, target.driver, dsn)
		})
	}
}

func testRollback(t *testing.T, driver, dsn string) {
	for _, failure := range []string{"reference", "business", "audit"} {
		t.Run(failure, func(t *testing.T) {
			f := openFixture(t, driver, dsn)
			original, replacement := f.file(t, "ready"), f.file(t, "ready")
			item := owner()
			if err := f.replace(t.Context(), item, original.ID); err != nil {
				t.Fatal(err)
			}
			unit := f.writes
			if failure == "audit" {
				var err error
				unit, err = uow.New(f.db, failingRecorder{})
				if err != nil {
					t.Fatal(err)
				}
			}
			err := unit.Run(t.Context(), func(tx *gorm.DB) (audit.Event, error) {
				if err := tx.Model(&f.user).Update("display_name", "after").Error; err != nil {
					return audit.Event{}, err
				}
				references, err := f.service.Bind(tx)
				if err != nil {
					return audit.Event{}, err
				}
				ids := []string{replacement.ID}
				if failure == "reference" {
					ids = append(ids, uuid.NewString())
				}
				if _, err := references.Replace(t.Context(), item, ids); err != nil {
					return audit.Event{}, err
				}
				if failure == "business" {
					return audit.Event{}, errors.New("business save failed")
				}
				return event(item), nil
			})
			if err == nil {
				t.Fatal("failed operation succeeded")
			}
			if got := f.referenced(t, item); !slices.Equal(got, []string{original.ID}) {
				t.Fatalf("references did not roll back: %v", got)
			}
			var user domain.User
			if err := f.db.First(&user, "id = ?", f.user.ID).Error; err != nil || user.DisplayName != "before" {
				t.Fatalf("business rollback user=%#v error=%v", user, err)
			}
			var count int64
			if err := f.db.Model(&domain.AuditLog{}).Where("resource_id = ?", item.ResourceID).Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("audit rollback count=%d error=%v", count, err)
			}
		})
	}
}

func TestReferencesRejectWrongAndEscapedTransactions(t *testing.T) {
	f, other := openFixture(t, "sqlite", ""), openFixture(t, "sqlite", "")
	if _, err := f.service.Bind(f.db); !errors.Is(err, frameworkfiles.ErrInvalidTransaction) {
		t.Fatalf("root bind error = %v", err)
	}
	var escaped frameworkfiles.References
	item := owner()
	err := f.writes.Run(t.Context(), func(tx *gorm.DB) (audit.Event, error) {
		if _, err := other.service.Bind(tx); !errors.Is(err, frameworkfiles.ErrInvalidTransaction) {
			t.Fatalf("cross database bind error = %v", err)
		}
		var err error
		escaped, err = f.service.Bind(tx)
		return event(item), err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.Replace(t.Context(), item, nil); !errors.Is(err, frameworkfiles.ErrInvalidTransaction) {
		t.Fatalf("escaped references error = %v", err)
	}
}

func TestReplaceReturnsVerifiedSortedMetadataAndSupportsRepeatedCalls(t *testing.T) {
	f := openFixture(t, "sqlite", "")
	a, b := f.file(t, "ready"), f.file(t, "ready")
	item := owner()
	err := f.writes.Run(t.Context(), func(tx *gorm.DB) (audit.Event, error) {
		references, err := f.service.Bind(tx.WithContext(t.Context()))
		if err != nil {
			return audit.Event{}, err
		}
		got, err := references.Replace(t.Context(), item, []string{b.ID, a.ID, b.ID})
		if err != nil {
			return audit.Event{}, err
		}
		want := []string{a.ID, b.ID}
		slices.Sort(want)
		if len(got) != 2 || got[0].ID != want[0] || got[1].ID != want[1] ||
			got[0].ContentType != "image/png" || got[0].SHA256 != a.SHA256 ||
			got[0].Width != 1 || got[0].Height != 1 || got[0].Size != 4 ||
			got[0].OwnerID != f.user.ID || got[0].Visibility != "private" {
			t.Fatalf("verified metadata = %#v", got)
		}
		_, err = references.Replace(t.Context(), item, []string{b.ID})
		return event(item), err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.referenced(t, item); !slices.Equal(got, []string{b.ID}) {
		t.Fatalf("repeated replacement set = %v", got)
	}
}

func TestReplaceSavepointRestoresRowsAfterLateWriteFailure(t *testing.T) {
	f := openFixture(t, "sqlite", "")
	a, b := f.file(t, "ready"), f.file(t, "ready")
	item := owner()
	if err := f.replace(t.Context(), item, a.ID); err != nil {
		t.Fatal(err)
	}
	// Force the final manifest update to fail after the reference rows have
	// already changed, then deliberately handle the error in the outer write.
	if err := f.db.Exec(`CREATE TRIGGER reject_reference_manifest BEFORE UPDATE ON file_reference_owners
		BEGIN SELECT RAISE(ABORT, 'manifest write unavailable'); END`).Error; err != nil {
		t.Fatal(err)
	}
	err := f.writes.Run(t.Context(), func(tx *gorm.DB) (audit.Event, error) {
		references, err := f.service.Bind(tx)
		if err != nil {
			return audit.Event{}, err
		}
		if _, err := references.Replace(t.Context(), item, []string{b.ID}); err == nil {
			t.Fatal("late write failure unexpectedly succeeded")
		}
		return event(item), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.referenced(t, item); !slices.Equal(got, []string{a.ID}) {
		t.Fatalf("failed replacement leaked partial writes: %v", got)
	}
}

func TestReferencesFailClosedWhenOwnerManifestDiffers(t *testing.T) {
	f := openFixture(t, "sqlite", "")
	file := f.file(t, "ready")
	item := owner()
	if err := f.replace(t.Context(), item, file.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&filereferences.Owner{}).Where("resource = ? AND resource_id = ?", item.Resource, item.ResourceID).Update("file_ids", "[]").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.replace(t.Context(), item); !errors.Is(err, frameworkfiles.ErrInvalidState) {
		t.Fatalf("corrupt owner error = %v", err)
	}
	if got := f.referenced(t, item); !slices.Equal(got, []string{file.ID}) {
		t.Fatalf("corrupt owner changed references: %v", got)
	}
}

func TestOpenKeepsOriginalStorageAndFailsClosed(t *testing.T) {
	f := openFixture(t, "sqlite", "")
	file := f.file(t, "ready")
	rootA, rootB := t.TempDir(), t.TempDir()
	profileA := config.SynthesizeStorageProfile(config.Storage{Driver: "local", LocalRoot: rootA})
	profileB := config.SynthesizeStorageProfile(config.Storage{Driver: "local", LocalRoot: rootB})
	for root, body := range map[string]string{rootA: "from A", rootB: "from B"} {
		if err := os.MkdirAll(filepath.Join(root, "files"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, file.ObjectKey), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&file).Update("storage_profile_id", profileA.ID).Error; err != nil {
		t.Fatal(err)
	}
	service := serviceForProfiles(t, f.db, profileB.ID, []config.StorageProfile{profileA, profileB})
	metadata, reader, err := service.Open(t.Context(), file.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil || string(body) != "from A" || metadata.ID != file.ID || metadata.OwnerID != f.user.ID {
		t.Fatalf("original read metadata=%#v body=%q error=%v close=%v", metadata, body, err, closeErr)
	}
	missing := serviceForProfiles(t, f.db, profileB.ID, []config.StorageProfile{profileB})
	if _, _, err := missing.Open(t.Context(), file.ID); !errors.Is(err, frameworkfiles.ErrStorageUnavailable) {
		t.Fatalf("missing profile error = %v", err)
	}
	brokenRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(brokenRoot, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	profileA.LocalRoot = brokenRoot
	degraded := serviceForProfiles(t, f.db, profileB.ID, []config.StorageProfile{profileA, profileB})
	if _, _, err := degraded.Open(t.Context(), file.ID); !errors.Is(err, frameworkfiles.ErrStorageUnavailable) {
		t.Fatalf("degraded profile error = %v", err)
	}
	if err := f.db.Model(&file).Update("storage_profile_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Open(t.Context(), file.ID); !errors.Is(err, frameworkfiles.ErrStorageUnavailable) {
		t.Fatalf("absent profile identity error = %v", err)
	}
}

func serviceForProfiles(t *testing.T, db *gorm.DB, active string, profiles []config.StorageProfile) frameworkfiles.Service {
	t.Helper()
	installation, err := config.NewManagedInstallation(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "unused.db")}, strings.Repeat("test-secret-", 4))
	if err != nil {
		t.Fatal(err)
	}
	installation.Profiles, installation.ActiveProfileID = profiles, active
	state, err := config.ResolveState(map[string]string{"AGINEX_ENV": "test"}, filepath.Join(t.TempDir(), "config.json"), &installation)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := storage.NewRegistry(t.Context(), state.Config, recorder)
	if err != nil {
		t.Fatal(err)
	}
	service, err := fileservice.New(db, registry)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
