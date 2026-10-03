package uow

import (
	"context"
	"errors"
	"testing"

	"github.com/xgtian-root/aginex/server/framework/audit"
	"gorm.io/gorm"
)

func TestRequireTransactionOnlyAcceptsActiveSameDatabaseRun(t *testing.T) {
	db := openTestDatabase(t)
	other := openTestDatabase(t)
	unit, err := New(db, databaseRecorder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireTransaction(db, db); !errors.Is(err, ErrActiveTransactionRequired) {
		t.Fatalf("root handle error = %v", err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := RequireTransaction(db, tx); !errors.Is(err, ErrActiveTransactionRequired) {
			t.Fatalf("ordinary transaction error = %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var escaped *gorm.DB
	err = unit.Run(t.Context(), func(tx *gorm.DB) (audit.Event, error) {
		escaped = tx.WithContext(context.Background())
		if err := RequireTransaction(db, escaped); err != nil {
			t.Fatalf("active cloned transaction error = %v", err)
		}
		if err := RequireTransaction(other, tx); !errors.Is(err, ErrActiveTransactionRequired) {
			t.Fatalf("wrong database error = %v", err)
		}
		if err := RequireTransaction(db, db.WithContext(tx.Statement.Context)); !errors.Is(err, ErrActiveTransactionRequired) {
			t.Fatalf("root sharing transaction context error = %v", err)
		}
		return successEvent(), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireTransaction(db, escaped); !errors.Is(err, ErrActiveTransactionRequired) {
		t.Fatalf("escaped transaction error = %v", err)
	}
}

func TestRequireTransactionRejectsEarlyTransactionCompletion(t *testing.T) {
	for _, commit := range []bool{false, true} {
		db := openTestDatabase(t)
		unit, err := New(db, databaseRecorder{})
		if err != nil {
			t.Fatal(err)
		}
		err = unit.Run(t.Context(), func(tx *gorm.DB) (audit.Event, error) {
			if commit {
				err = tx.Commit().Error
			} else {
				err = tx.Rollback().Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := RequireTransaction(db, tx); !errors.Is(err, ErrActiveTransactionRequired) {
				t.Fatalf("completed transaction error = %v", err)
			}
			return successEvent(), nil
		})
		if err == nil {
			t.Fatal("prematurely ended Run succeeded")
		}
	}
}
