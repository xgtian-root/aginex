package uow

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/xgtian-root/aginex/framework/audit"
	"gorm.io/gorm"
)

var errAuditUnavailable = errors.New("audit unavailable")

type auditRow struct {
	ID       int64  `gorm:"primaryKey"`
	Action   string `gorm:"column:action"`
	Resource string `gorm:"column:resource"`
}

type businessRow struct {
	ID   int64  `gorm:"primaryKey"`
	Name string `gorm:"column:name"`
}

type databaseRecorder struct {
	err error
}

func (r databaseRecorder) Record(ctx context.Context, tx *gorm.DB, event audit.Event) error {
	if r.err != nil {
		return r.err
	}
	return tx.WithContext(ctx).Create(&auditRow{
		Action:   event.Action,
		Resource: event.Resource,
	}).Error
}

func TestRunCommitsMutationAndAuditTogether(t *testing.T) {
	db := openTestDatabase(t)
	unit, err := New(db, databaseRecorder{})
	if err != nil {
		t.Fatal(err)
	}

	err = unit.Run(context.Background(), func(tx *gorm.DB) (audit.Event, error) {
		if err := tx.Create(&businessRow{Name: "Agent Desk"}).Error; err != nil {
			return audit.Event{}, err
		}
		return successEvent(), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := countRows[businessRow](t, db); got != 1 {
		t.Fatalf("business rows = %d, want 1", got)
	}
	if got := countRows[auditRow](t, db); got != 1 {
		t.Fatalf("audit rows = %d, want 1", got)
	}
}

func TestRunRollsBackMutationWhenAuditFails(t *testing.T) {
	db := openTestDatabase(t)
	unit, err := New(db, databaseRecorder{err: errAuditUnavailable})
	if err != nil {
		t.Fatal(err)
	}

	err = unit.Run(context.Background(), func(tx *gorm.DB) (audit.Event, error) {
		if err := tx.Create(&businessRow{Name: "Agent Desk"}).Error; err != nil {
			return audit.Event{}, err
		}
		return successEvent(), nil
	})
	if !errors.Is(err, errAuditUnavailable) {
		t.Fatalf("error = %v, want %v", err, errAuditUnavailable)
	}
	if got := countRows[businessRow](t, db); got != 0 {
		t.Fatalf("business rows = %d, want 0", got)
	}
	if got := countRows[auditRow](t, db); got != 0 {
		t.Fatalf("audit rows = %d, want 0", got)
	}
}

func TestRunDoesNotAuditFailedMutation(t *testing.T) {
	db := openTestDatabase(t)
	unit, err := New(db, databaseRecorder{})
	if err != nil {
		t.Fatal(err)
	}
	mutationErr := errors.New("mutation failed")

	err = unit.Run(context.Background(), func(*gorm.DB) (audit.Event, error) {
		return audit.Event{}, mutationErr
	})
	if !errors.Is(err, mutationErr) {
		t.Fatalf("error = %v, want %v", err, mutationErr)
	}
	if got := countRows[auditRow](t, db); got != 0 {
		t.Fatalf("audit rows = %d, want 0", got)
	}
}

func TestRunRejectsMissingAuditMetadata(t *testing.T) {
	db := openTestDatabase(t)
	unit, err := New(db, databaseRecorder{})
	if err != nil {
		t.Fatal(err)
	}

	err = unit.Run(context.Background(), func(tx *gorm.DB) (audit.Event, error) {
		if err := tx.Create(&businessRow{Name: "Agent Desk"}).Error; err != nil {
			return audit.Event{}, err
		}
		return audit.Event{}, nil
	})
	if !errors.Is(err, audit.ErrActorKindRequired) {
		t.Fatalf("error = %v, want %v", err, audit.ErrActorKindRequired)
	}
	if got := countRows[businessRow](t, db); got != 0 {
		t.Fatalf("business rows = %d, want 0", got)
	}
}

func TestRunRollsBackMutationWhenAuditContainsSensitiveFields(t *testing.T) {
	db := openTestDatabase(t)
	unit, err := New(db, databaseRecorder{})
	if err != nil {
		t.Fatal(err)
	}

	err = unit.Run(context.Background(), func(tx *gorm.DB) (audit.Event, error) {
		if err := tx.Create(&businessRow{Name: "Agent Desk"}).Error; err != nil {
			return audit.Event{}, err
		}
		event := successEvent()
		event.After = audit.SanitizedFields{"passwordHash": "must-not-be-recorded"}
		return event, nil
	})
	if !errors.Is(err, audit.ErrSensitiveField) {
		t.Fatalf("error = %v, want %v", err, audit.ErrSensitiveField)
	}
	if got := countRows[businessRow](t, db); got != 0 {
		t.Fatalf("business rows = %d, want 0", got)
	}
	if got := countRows[auditRow](t, db); got != 0 {
		t.Fatalf("audit rows = %d, want 0", got)
	}
}

func openTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "uow.db")))
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE business_rows (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL)`,
		`CREATE TABLE audit_rows (id INTEGER PRIMARY KEY AUTOINCREMENT, action TEXT NOT NULL, resource TEXT NOT NULL)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func successEvent() audit.Event {
	return audit.Event{
		ActorKind: audit.ActorSystem,
		Action:    "products:create",
		Resource:  "product",
		Result:    audit.ResultSuccess,
		Source:    audit.SourceWorker,
	}
}

func countRows[T any](t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	var model T
	if err := db.Model(&model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}
