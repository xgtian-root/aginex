package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/server/framework/audit"
	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	frameworkfiles "github.com/xgtian-root/aginex/server/framework/files"
	"github.com/xgtian-root/aginex/server/framework/observability"
	"github.com/xgtian-root/aginex/server/internal/auth"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestUploadRejectionPreservesUnexpectedPendingReferences(t *testing.T) {
	_, db, server, cookie := newFileHandlerTestApp(t)
	prepared, _ := createPendingLocalUpload(t, server, cookie, validPNG(t))
	// Public references reject pending files. Seed an inconsistent draft to
	// verify quarantine still fails closed before marking invalid or enqueueing.
	if err := db.Exec("INSERT INTO file_reference_owners(resource, resource_id, file_ids) VALUES (?, ?, ?)", "articles", "one", `["`+prepared.File.ID+`"]`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO file_references(resource, resource_id, file_id) VALUES (?, ?, ?)", "articles", "one", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	queue := &stubTransactionalQueue{}
	server.jobs = queue
	var file domain.FileObject
	if err := db.First(&file, "id = ?", prepared.File.ID).Error; err != nil {
		t.Fatal(err)
	}
	var user domain.User
	if err := db.First(&user, "id = ?", file.OwnerID).Error; err != nil {
		t.Fatal(err)
	}
	request, _ := gin.CreateTestContext(httptest.NewRecorder())
	request.Request = httptest.NewRequest(http.MethodPost, "/api/v1/files/"+file.ID+"/confirm", nil).WithContext(t.Context())
	request.Set(principalKey, auth.Principal{User: user, GrantScopes: map[string]frameworkauthz.GrantScope{"files:create": frameworkauthz.ScopeAll}})
	if err := server.markInvalidUpload(request, file); !errors.Is(err, frameworkfiles.ErrInUse) {
		t.Fatalf("rejection = %v, want ErrInUse", err)
	}
	if err := db.First(&file, "id = ?", file.ID).Error; err != nil {
		t.Fatal(err)
	}
	var rejections int64
	if err := db.Model(&domain.AuditLog{}).Where("action = ? AND resource_id = ?", "files:reject", file.ID).Count(&rejections).Error; err != nil {
		t.Fatal(err)
	}
	if file.Status != "pending" || rejections != 0 || len(queue.requests) != 0 {
		t.Fatalf("protected rejection changed status/audits/jobs: %s/%d/%d", file.Status, rejections, len(queue.requests))
	}
}

func TestPostgresLateUploadRejectionCannotInvalidateReferencedReadyFile(t *testing.T) {
	dsn := os.Getenv("AGINEX_FILES_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AGINEX_FILES_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := "aginex_rejection_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop isolated rejection test schema: %v", err)
		}
	})
	scopedDSN := dsn + " search_path=" + schema + " application_name=" + schema
	if parsed, err := url.Parse(dsn); err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		query.Set("application_name", schema)
		parsed.RawQuery = query.Encode()
		scopedDSN = parsed.String()
	}
	cfg := config.WithDefaults(config.Config{Environment: "test", Database: config.Database{Driver: "postgres", DSN: scopedDSN}, Storage: config.Storage{Driver: "local", LocalRoot: t.TempDir()}})
	db, err := database.Open(cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := MigrateUp(t.Context(), sqlDB, cfg, FilesModule()); err != nil {
		t.Fatal(err)
	}
	recorder, err := observability.NewRecorder(nil)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewCompositionWithModulesAndObservability(cfg, db, recorder, FilesModule())
	if err != nil {
		t.Fatal(err)
	}
	user := domain.User{ID: uuid.NewString(), Email: "rejection@example.test", DisplayName: "Test", Status: "active"}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	profile, ok := server.storageRegistry.ActiveProfile()
	if !ok {
		t.Fatal("active storage profile is missing")
	}
	file := domain.FileObject{ID: uuid.NewString(), StorageProfileID: &profile.ID, Provider: "local", ObjectKey: "files/late-rejection", OriginalName: "ready.bin", ContentType: "application/octet-stream", Size: 1, OwnerID: user.ID, Visibility: "private", Status: "pending"}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	queue := &stubTransactionalQueue{}
	server.jobs = queue
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, _ := gin.CreateTestContext(httptest.NewRecorder())
	request.Request = httptest.NewRequest(http.MethodPost, "/api/v1/files/"+file.ID+"/confirm", nil).WithContext(ctx)
	request.Set(principalKey, auth.Principal{User: user, GrantScopes: map[string]frameworkauthz.GrantScope{"files:create": frameworkauthz.ScopeAll}})

	locked := make(chan struct{})
	complete := make(chan struct{})
	confirmation := make(chan error, 1)
	go func() {
		confirmation <- server.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
			var current domain.FileObject
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", file.ID).Error; err != nil {
				return frameworkaudit.Event{}, err
			}
			close(locked)
			select {
			case <-complete:
			case <-ctx.Done():
				return frameworkaudit.Event{}, ctx.Err()
			}
			if err := tx.Model(&current).Updates(map[string]any{"status": "ready", "sha256": strings.Repeat("a", 64)}).Error; err != nil {
				return frameworkaudit.Event{}, err
			}
			references, err := server.services.Files.Bind(tx)
			if err != nil {
				return frameworkaudit.Event{}, err
			}
			if _, err := references.Replace(ctx, frameworkfiles.ReferenceOwner{Resource: "articles", ResourceID: "one"}, []string{file.ID}); err != nil {
				return frameworkaudit.Event{}, err
			}
			return frameworkaudit.Event{ActorKind: frameworkaudit.ActorSystem, Action: "articles:update", Resource: "articles", ResourceID: "one", Result: frameworkaudit.ResultSuccess, Source: frameworkaudit.SourceSystem}, nil
		})
	}()
	select {
	case <-locked:
	case err := <-confirmation:
		t.Fatalf("confirmation before file lock: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	rejection := make(chan error, 1)
	go func() { rejection <- server.markInvalidUpload(request, file) }()
	// Both the buggy unguarded UPDATE and the corrected SELECT FOR UPDATE
	// must wait on the confirmation lock. Observe that wait before committing
	// ready+references, so this reproduces the race without a scheduling sleep.
	for {
		var waiting int
		if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock' AND query LIKE '%file_objects%'", schema).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-rejection:
			t.Fatalf("rejection did not wait on the file lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(complete)
	if err := <-confirmation; err != nil {
		t.Fatal(err)
	}
	if err := <-rejection; !errors.Is(err, errUploadIntentChanged) {
		t.Fatalf("late rejection = %v, want state conflict", err)
	}
	if _, err := server.services.Files.GetReady(ctx, file.ID); err != nil {
		t.Fatalf("late rejection damaged the ready file: %v", err)
	}
	var references, rejections int64
	if err := db.Table("file_references").Where("file_id = ?", file.ID).Count(&references).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.AuditLog{}).Where("action = ? AND resource_id = ?", "files:reject", file.ID).Count(&rejections).Error; err != nil {
		t.Fatal(err)
	}
	if references != 1 || rejections != 0 || len(queue.requests) != 0 {
		t.Fatalf("late rejection changed references/audits/jobs: %d/%d/%d", references, rejections, len(queue.requests))
	}
}
