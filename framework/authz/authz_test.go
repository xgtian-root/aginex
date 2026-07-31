package authz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const filesRead = "files:read"

type ownedRecord struct {
	ID      string
	OwnerID string
}

func TestCheckDistinguishesUnauthenticatedAndForbidden(t *testing.T) {
	authorizer := NewAuthorizer()
	policy := newOwnerPolicy(t)
	if err := authorizer.Register("files", policy); err != nil {
		t.Fatal(err)
	}

	resource := ResourceRef{Resource: "files", ID: "file-1", OwnerID: "user-1"}
	if err := authorizer.Check(context.Background(), Actor{}, filesRead, resource); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("anonymous check error = %v, want ErrUnauthenticated", err)
	}

	withoutGrant := NewUserActor("user-1")
	if err := authorizer.Check(context.Background(), withoutGrant, filesRead, resource); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing grant error = %v, want ErrForbidden", err)
	}

	otherOwner := NewUserActor("user-2", Grant{Permission: filesRead, Scope: ScopeOwn})
	if err := authorizer.Check(context.Background(), otherOwner, filesRead, resource); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other owner error = %v, want ErrForbidden", err)
	}

	owner := NewUserActor("user-1", Grant{Permission: filesRead, Scope: ScopeOwn})
	if err := authorizer.Check(context.Background(), owner, filesRead, resource); err != nil {
		t.Fatalf("owner check error = %v", err)
	}

	all := NewUserActor("admin-1", Grant{Permission: filesRead, Scope: ScopeAll})
	if err := authorizer.Check(context.Background(), all, filesRead, resource); err != nil {
		t.Fatalf("all-scope check error = %v", err)
	}
}

func TestUnknownPolicyDeniesByDefault(t *testing.T) {
	authorizer := NewAuthorizer()
	actor := NewUserActor("user-1", Grant{Permission: filesRead, Scope: ScopeAll})
	resource := ResourceRef{Resource: "files", ID: "file-1", OwnerID: "user-1"}

	if err := authorizer.Check(context.Background(), actor, filesRead, resource); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown check policy error = %v, want ErrForbidden", err)
	}

	query := dryRunDB(t).Model(&ownedRecord{})
	if _, err := authorizer.Scope(context.Background(), actor, filesRead, "files", query); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unknown scope policy error = %v, want ErrForbidden", err)
	}
}

func TestOwnerColumnPolicyScopesQueriesInSQL(t *testing.T) {
	authorizer := NewAuthorizer()
	if err := authorizer.Register("files", newOwnerPolicy(t)); err != nil {
		t.Fatal(err)
	}

	ownActor := NewUserActor("user-1", Grant{Permission: filesRead, Scope: ScopeOwn})
	ownQuery, err := authorizer.Scope(
		context.Background(),
		ownActor,
		filesRead,
		"files",
		dryRunDB(t).Model(&ownedRecord{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	ownResult := ownQuery.Find(&[]ownedRecord{})
	ownSQL := ownResult.Statement.SQL.String()
	if !strings.Contains(ownSQL, "owner_id") {
		t.Fatalf("own SQL = %q, want owner column predicate", ownSQL)
	}
	if len(ownResult.Statement.Vars) != 1 || ownResult.Statement.Vars[0] != "user-1" {
		t.Fatalf("own SQL vars = %#v, want user-1", ownResult.Statement.Vars)
	}

	allActor := NewUserActor("admin-1", Grant{Permission: filesRead, Scope: ScopeAll})
	allQuery, err := authorizer.Scope(
		context.Background(),
		allActor,
		filesRead,
		"files",
		dryRunDB(t).Model(&ownedRecord{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	allResult := allQuery.Find(&[]ownedRecord{})
	if allSQL := allResult.Statement.SQL.String(); strings.Contains(allSQL, "owner_id") {
		t.Fatalf("all SQL = %q, want no owner predicate", allSQL)
	}
}

func TestSystemActorRequiresExplicitPolicyPermission(t *testing.T) {
	system := NewSystemActor("file-worker")
	resource := ResourceRef{Resource: "files", ID: "file-1", OwnerID: "user-1"}

	defaultAuthorizer := NewAuthorizer()
	if err := defaultAuthorizer.Register("files", newOwnerPolicy(t)); err != nil {
		t.Fatal(err)
	}
	if err := defaultAuthorizer.Check(context.Background(), system, filesRead, resource); !errors.Is(err, ErrForbidden) {
		t.Fatalf("default system error = %v, want ErrForbidden", err)
	}

	internalAuthorizer := NewAuthorizer()
	internalPolicy := newOwnerPolicy(t, WithSystemPermissions(filesRead))
	if err := internalAuthorizer.Register("files", internalPolicy); err != nil {
		t.Fatal(err)
	}
	if err := internalAuthorizer.Check(context.Background(), system, filesRead, resource); err != nil {
		t.Fatalf("explicit system check error = %v", err)
	}
	systemQuery, err := internalAuthorizer.Scope(
		context.Background(),
		system,
		filesRead,
		"files",
		dryRunDB(t).Model(&ownedRecord{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result := systemQuery.Find(&[]ownedRecord{})
	if sql := result.Statement.SQL.String(); strings.Contains(sql, "owner_id") {
		t.Fatalf("system SQL = %q, want explicitly unscoped internal access", sql)
	}
}

func newOwnerPolicy(t *testing.T, options ...OwnerColumnOption) *OwnerColumnPolicy {
	t.Helper()
	policy, err := NewOwnerColumnPolicy("owner_id", options...)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func dryRunDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	return db
}
