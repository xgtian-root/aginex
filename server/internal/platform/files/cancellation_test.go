package files_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/xgtian-root/aginex/server/framework/audit"
	"github.com/xgtian-root/aginex/server/internal/platform/filereferences"
	"gorm.io/gorm"
)

func TestReplaceSavepointSurvivesChildContextCancellation(t *testing.T) {
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
			for _, bindChild := range []bool{false, true} {
				name := "bind_parent"
				if bindChild {
					name = "bind_child"
				}
				t.Run(name, func(t *testing.T) {
					f := openFixture(t, target.driver, dsn)
					a, b := f.file(t, "ready"), f.file(t, "ready")
					item := owner()
					if err := f.replace(t.Context(), item, a.ID); err != nil {
						t.Fatal(err)
					}
					child, cancel := context.WithCancel(t.Context())
					defer cancel()
					// The reference rows have already changed when the child is
					// canceled. The final owner-manifest write must then fail.
					if err := f.db.Callback().Create().After("gorm:create").Register("test:cancel_after_reference", func(tx *gorm.DB) {
						if tx.Statement.Table == "file_references" && tx.Error == nil {
							cancel()
						}
					}); err != nil {
						t.Fatal(err)
					}
					err := f.writes.Run(t.Context(), func(tx *gorm.DB) (audit.Event, error) {
						bindTx := tx
						if bindChild {
							bindTx = tx.WithContext(child)
						}
						references, err := f.service.Bind(bindTx)
						if err != nil {
							return audit.Event{}, err
						}
						if _, err := references.Replace(child, item, []string{b.ID}); !errors.Is(err, context.Canceled) {
							t.Fatalf("Replace error = %v, want context cancellation", err)
						}
						// Handling the local error is supported: the outer business
						// change and its audit may still commit after full restoration.
						if err := tx.Model(&f.user).Update("display_name", "handled cancellation").Error; err != nil {
							return audit.Event{}, err
						}
						return event(item), nil
					})
					if err != nil {
						t.Fatal(err)
					}
					if got := f.referenced(t, item); !slices.Equal(got, []string{a.ID}) {
						t.Fatalf("canceled replacement leaked partial references: %v", got)
					}
					var manifest filereferences.Owner
					if err := f.db.Where("resource = ? AND resource_id = ?", item.Resource, item.ResourceID).First(&manifest).Error; err != nil {
						t.Fatal(err)
					}
					if manifest.FileIDs != `["`+a.ID+`"]` {
						t.Fatalf("canceled replacement changed manifest: %s", manifest.FileIDs)
					}
					if err := f.db.First(&f.user, "id = ?", f.user.ID).Error; err != nil {
						t.Fatal(err)
					}
					if f.user.DisplayName != "handled cancellation" {
						t.Fatalf("outer transaction did not commit: %s", f.user.DisplayName)
					}
				})
			}
		})
	}
}
