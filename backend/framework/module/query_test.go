package module

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/xgtian-root/aginex/backend/framework/authz"
	"gorm.io/gorm"
)

type queryCapabilityRow struct {
	ID      string `gorm:"primaryKey"`
	OwnerID string
	Name    string
}

func TestQueryCapabilityRequiresActualScopedExecution(t *testing.T) {
	db := openQueryCapabilityDatabase(t)
	decision := newQueryCapabilityDecision(t)
	ctx, err := ContextWithRequestAuthorization(t.Context(), decision)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = ContextWithRequestAuthorizationMode(
		ctx,
		AuthorizationQuery,
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := NewQueryDatabase(db.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	query := database.Model(&queryCapabilityRow{})
	if _, err := decision.Scope(ctx, query); err != nil {
		t.Fatal(err)
	}
	if decision.Satisfies(AuthorizationQuery) {
		t.Fatal("discarded scoped query satisfied authorization")
	}

	var leaked []queryCapabilityRow
	result := query.Order("id").Find(&leaked)
	if !errors.Is(result.Error, ErrAuthorizationQueryUnscoped) {
		t.Fatalf("unscoped query error = %v", result.Error)
	}
	if len(leaked) != 0 {
		t.Fatalf("unscoped query returned %#v", leaked)
	}
	if !decision.Violated() {
		t.Fatal("unscoped query did not permanently record a violation")
	}
}

func TestQueryCapabilityRetainsScopeAcrossAllBuilders(t *testing.T) {
	db := openQueryCapabilityDatabase(t)
	decision := newQueryCapabilityDecision(t)
	ctx, err := ContextWithRequestAuthorization(t.Context(), decision)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = ContextWithRequestAuthorizationMode(
		ctx,
		AuthorizationQuery,
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := NewQueryDatabase(db.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := decision.Scope(
		ctx,
		database.Model(&queryCapabilityRow{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if result := scoped.Where(
		"name <> ?",
		"missing",
	).Order("id").Offset(0).Limit(10).Count(&count); result.Error != nil {
		t.Fatal(result.Error)
	}
	if count != 1 {
		t.Fatalf("scoped count = %d, want 1", count)
	}
	var rows []queryCapabilityRow
	if result := scoped.Select(
		"id",
		"owner_id",
		"name",
	).Find(&rows); result.Error != nil {
		t.Fatal(result.Error)
	}
	if len(rows) != 1 ||
		rows[0].OwnerID != "user-a" ||
		rows[0].Name != "visible" {
		t.Fatalf("scoped rows = %#v", rows)
	}
	if !decision.Satisfies(AuthorizationQuery) {
		t.Fatal("executed scoped query did not satisfy authorization")
	}
}

func TestQueryCapabilityAppliesPolicyAfterCallerOrConditions(
	t *testing.T,
) {
	db := openQueryCapabilityDatabase(t)
	decision := newQueryCapabilityDecision(t)
	ctx, err := ContextWithRequestAuthorization(t.Context(), decision)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err = ContextWithRequestAuthorizationMode(
		ctx,
		AuthorizationQuery,
	)
	if err != nil {
		t.Fatal(err)
	}
	database, err := NewQueryDatabase(db.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := decision.Scope(
		ctx,
		database.Model(&queryCapabilityRow{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	var rows []queryCapabilityRow
	if result := scoped.Or("1 = 1").Order("id").Find(&rows); result.Error != nil {
		t.Fatal(result.Error)
	}
	if len(rows) != 1 || rows[0].OwnerID != "user-a" {
		t.Fatalf("late-applied policy rows = %#v", rows)
	}
}

func TestQueryCapabilityHasNoDatabaseOrMutationEscape(t *testing.T) {
	queryType := reflect.TypeFor[Query]()
	databaseType := reflect.TypeFor[*QueryDatabase]()
	for _, forbidden := range []string{
		"Association",
		"Create",
		"DB",
		"Delete",
		"Exec",
		"Migrator",
		"Raw",
		"Save",
		"Session",
		"Transaction",
		"Unscoped",
		"Update",
		"Updates",
		"WithContext",
	} {
		if _, ok := queryType.MethodByName(forbidden); ok {
			t.Fatalf("Query exposes forbidden method %q", forbidden)
		}
		if _, ok := databaseType.MethodByName(forbidden); ok {
			t.Fatalf("QueryDatabase exposes forbidden method %q", forbidden)
		}
	}
}

func openQueryCapabilityDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(
		sqlite.Open(filepath.Join(t.TempDir(), "query.db")),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TABLE query_capability_rows (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL
		)
	`).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []queryCapabilityRow{
		{ID: "a", OwnerID: "user-a", Name: "visible"},
		{ID: "b", OwnerID: "user-b", Name: "secret"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func newQueryCapabilityDecision(t *testing.T) RequestAuthorization {
	t.Helper()
	policy, err := authz.NewOwnerColumnPolicy("owner_id")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := NewRequestAuthorization(
		authz.NewUserActor("user-a", authz.Grant{
			Permission: "items:read",
			Scope:      authz.ScopeOwn,
		}),
		"items:read",
		authz.ScopeOwn,
		policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	return decision
}
