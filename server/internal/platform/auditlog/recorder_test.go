package auditlog

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/xgtian-root/aginex/server/framework/audit"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"gorm.io/gorm"
)

var errRollback = errors.New("roll back")

func TestRecorderUsesProvidedTransaction(t *testing.T) {
	db := openRecorderDatabase(t)

	actorID := "user-1"
	rollback := db.Transaction(func(tx *gorm.DB) error {
		if err := (Recorder{}).Record(context.Background(), tx, testEvent(&actorID)); err != nil {
			return err
		}
		return errRollback
	})
	if rollback == nil {
		t.Fatal("expected transaction rollback")
	}

	var count int64
	if err := db.Model(&domain.AuditLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("audit rows = %d, want 0", count)
	}
}

func TestRecorderPersistsProductionAuditContext(t *testing.T) {
	db := openRecorderDatabase(t)
	actorID := "user-1"
	event := testEvent(&actorID)
	event.Before = audit.SanitizedFields{"status": "draft"}
	event.After = audit.SanitizedFields{"status": "active", "tags": []any{"featured"}}

	if err := db.Transaction(func(tx *gorm.DB) error {
		return (Recorder{}).Record(context.Background(), tx, event)
	}); err != nil {
		t.Fatal(err)
	}

	var stored domain.AuditLog
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ActorKind != audit.ActorUser || stored.Result != audit.ResultSuccess || stored.Source != audit.SourceHTTP {
		t.Fatalf("stored context = actor %q result %q source %q", stored.ActorKind, stored.Result, stored.Source)
	}
	if !reflect.DeepEqual(stored.Before, event.Before) {
		t.Fatalf("before = %#v, want %#v", stored.Before, event.Before)
	}
	if !reflect.DeepEqual(stored.After, event.After) {
		t.Fatalf("after = %#v, want %#v", stored.After, event.After)
	}
}

func TestRecorderRejectsSensitiveAuditFields(t *testing.T) {
	db := openRecorderDatabase(t)
	actorID := "user-1"
	event := testEvent(&actorID)
	event.After = audit.SanitizedFields{
		"credentials": map[string]any{"password": "must-not-be-recorded"},
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		return (Recorder{}).Record(context.Background(), tx, event)
	})
	if !errors.Is(err, audit.ErrSensitiveField) {
		t.Fatalf("error = %v, want ErrSensitiveField", err)
	}
	var count int64
	if err := db.Model(&domain.AuditLog{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("audit rows = %d, want 0", count)
	}
}

func openRecorderDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE audit_logs (
				id TEXT PRIMARY KEY,
				actor_id TEXT,
				actor_kind TEXT NOT NULL DEFAULT 'user',
				action TEXT NOT NULL,
				resource TEXT NOT NULL,
				resource_id TEXT NOT NULL DEFAULT '',
				result TEXT NOT NULL DEFAULT 'success',
				source TEXT NOT NULL DEFAULT 'http',
				summary TEXT NOT NULL DEFAULT '',
				request_id TEXT NOT NULL DEFAULT '',
				ip_address TEXT NOT NULL DEFAULT '',
				sanitized_before TEXT NOT NULL DEFAULT '{}',
				sanitized_after TEXT NOT NULL DEFAULT '{}',
				created_at DATETIME NOT NULL
			)
	`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func testEvent(actorID *string) audit.Event {
	return audit.Event{
		ActorID:    actorID,
		ActorKind:  audit.ActorUser,
		Action:     "products:create",
		Resource:   "product",
		ResourceID: "product-1",
		Result:     audit.ResultSuccess,
		RequestID:  "request-1",
		Source:     audit.SourceHTTP,
		Summary:    "Created product",
		IPAddress:  "127.0.0.1",
	}
}
