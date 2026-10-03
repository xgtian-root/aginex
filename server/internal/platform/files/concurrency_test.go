package files_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/server/framework/audit"
	frameworkfiles "github.com/xgtian-root/aginex/server/framework/files"
	"github.com/xgtian-root/aginex/server/internal/platform/filereferences"
	"gorm.io/gorm"
)

func TestReferenceConcurrency(t *testing.T) {
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
			f := openFixture(t, target.driver, dsn)
			t.Run("first_bindings_replace_entire_set", func(t *testing.T) {
				a, b := f.file(t, "ready"), f.file(t, "ready")
				item := owner()
				results := concurrent(t,
					func(ctx context.Context) error { return f.replace(ctx, item, a.ID) },
					func(ctx context.Context) error { return f.replace(ctx, item, b.ID) },
				)
				for _, err := range results {
					if err != nil {
						t.Fatal(err)
					}
				}
				got := f.referenced(t, item)
				if !slices.Equal(got, []string{a.ID}) && !slices.Equal(got, []string{b.ID}) {
					t.Fatalf("concurrent full replacement mixed sets: %v", got)
				}
			})
			t.Run("clear_and_replace", func(t *testing.T) {
				a, b := f.file(t, "ready"), f.file(t, "ready")
				item := owner()
				if err := f.replace(t.Context(), item, a.ID); err != nil {
					t.Fatal(err)
				}
				results := concurrent(t,
					func(ctx context.Context) error { return f.replace(ctx, item) },
					func(ctx context.Context) error { return f.replace(ctx, item, b.ID) },
				)
				for _, err := range results {
					if err != nil {
						t.Fatal(err)
					}
				}
				got := f.referenced(t, item)
				if len(got) != 0 && !slices.Equal(got, []string{b.ID}) {
					t.Fatalf("clear/replacement mixed sets: %v", got)
				}
			})
			t.Run("owners_exchange_files", func(t *testing.T) {
				a, b := f.file(t, "ready"), f.file(t, "ready")
				left, right := owner(), owner()
				if err := f.replace(t.Context(), left, a.ID); err != nil {
					t.Fatal(err)
				}
				if err := f.replace(t.Context(), right, b.ID); err != nil {
					t.Fatal(err)
				}
				results := concurrent(t,
					func(ctx context.Context) error { return f.replace(ctx, left, b.ID) },
					func(ctx context.Context) error { return f.replace(ctx, right, a.ID) },
				)
				for _, err := range results {
					if err != nil {
						t.Fatal(err)
					}
				}
				if !slices.Equal(f.referenced(t, left), []string{b.ID}) || !slices.Equal(f.referenced(t, right), []string{a.ID}) {
					t.Fatal("owner swap lost a reference")
				}
			})
			t.Run("delete_races_binding", func(t *testing.T) {
				for attempt := 0; attempt < 12; attempt++ {
					file := f.file(t, "ready")
					item := owner()
					results := concurrent(t,
						func(ctx context.Context) error { return f.replace(ctx, item, file.ID) },
						func(ctx context.Context) error { return f.delete(ctx, file.ID) },
					)
					bound, deleted := results[0], results[1]
					if bound == nil {
						if !errors.Is(deleted, frameworkfiles.ErrInUse) {
							t.Fatalf("bound reference was not protected: delete=%v", deleted)
						}
					} else if deleted != nil || !errors.Is(bound, frameworkfiles.ErrNotReady) {
						t.Fatalf("unexpected race outcome: bind=%v delete=%v", bound, deleted)
					}
				}
			})
			if target.driver != "sqlite" {
				t.Run("replace_after_earlier_snapshot", func(t *testing.T) { testEarlierSnapshot(t, f) })
			}
		})
	}
}

func concurrent(t *testing.T, operations ...func(context.Context) error) []error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	start := make(chan struct{})
	type result struct {
		index int
		err   error
	}
	done := make(chan result, len(operations))
	for index, operation := range operations {
		go func() {
			<-start
			done <- result{index: index, err: operation(ctx)}
		}()
	}
	close(start)
	results := make([]error, len(operations))
	for range operations {
		select {
		case result := <-done:
			results[result.index] = result.err
		case <-ctx.Done():
			t.Fatal("concurrent operations exceeded deadline")
		}
	}
	return results
}

func testEarlierSnapshot(t *testing.T, f *fixture) {
	t.Helper()
	a, b := f.file(t, "ready"), f.file(t, "ready")
	item := owner()
	if err := f.replace(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	snapshotTaken := make(chan struct{})
	continueReplacement := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- f.writes.Run(ctx, func(tx *gorm.DB) (audit.Event, error) {
			var count int64
			if err := tx.Model(&filereferences.Reference{}).Where("resource = ? AND resource_id = ?", item.Resource, item.ResourceID).Count(&count).Error; err != nil {
				return audit.Event{}, err
			}
			close(snapshotTaken)
			select {
			case <-continueReplacement:
			case <-ctx.Done():
				return audit.Event{}, ctx.Err()
			}
			references, err := f.service.Bind(tx)
			if err != nil {
				return audit.Event{}, err
			}
			_, err = references.Replace(ctx, item, []string{b.ID})
			return event(item), err
		})
	}()
	select {
	case <-snapshotTaken:
	case err := <-done:
		t.Fatalf("take snapshot: %v", err)
	case <-ctx.Done():
		t.Fatal("snapshot deadline exceeded")
	}
	if err := f.replace(ctx, item, a.ID); err != nil {
		close(continueReplacement)
		t.Fatal(err)
	}
	close(continueReplacement)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := f.referenced(t, item); !slices.Equal(got, []string{b.ID}) {
		t.Fatalf("earlier snapshot left stale references: %v", got)
	}
}
