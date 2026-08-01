package authz

import (
	"context"
	"errors"
	"testing"
)

func TestAllScopePolicyRequiresAllGrantAndExplicitSystemPermission(t *testing.T) {
	const permission = "products:read"
	policy, err := NewAllScopePolicy(WithSystemPermissions(permission))
	if err != nil {
		t.Fatal(err)
	}
	authorizer := NewAuthorizer()
	if err := authorizer.Register("products", policy); err != nil {
		t.Fatal(err)
	}
	resource := ResourceRef{Resource: "products", ID: "product-1"}

	if err := authorizer.Check(
		context.Background(),
		NewUserActor("user-1", Grant{Permission: permission, Scope: ScopeOwn}),
		permission,
		resource,
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("own-scope error = %v, want ErrForbidden", err)
	}
	if err := authorizer.Check(
		context.Background(),
		NewUserActor("user-1", Grant{Permission: permission, Scope: ScopeAll}),
		permission,
		resource,
	); err != nil {
		t.Fatalf("all-scope check: %v", err)
	}
	if err := authorizer.Check(
		context.Background(),
		NewSystemActor("worker-1"),
		permission,
		resource,
	); err != nil {
		t.Fatalf("system check: %v", err)
	}
	if err := authorizer.Check(
		context.Background(),
		NewSystemActor("worker-1"),
		"products:update",
		ResourceRef{Resource: "products", ID: "product-1"},
	); !errors.Is(err, ErrForbidden) {
		t.Fatalf("unlisted system permission error = %v, want ErrForbidden", err)
	}
}

func TestAllScopePolicyRejectsNilQuery(t *testing.T) {
	policy, err := NewAllScopePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Scope(
		context.Background(),
		NewUserActor("user-1"),
		ScopeAll,
		"products:read",
		nil,
	); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("nil query error = %v, want ErrInvalidPolicy", err)
	}
}

var _ Policy = (*AllScopePolicy)(nil)
