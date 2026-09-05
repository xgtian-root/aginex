package authz

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// AllScopePolicy protects resources that do not have per-user ownership.
// Human actors need an explicit all-scope grant. System actors remain denied
// unless the permission is explicitly listed at construction time.
type AllScopePolicy struct {
	systemPermissions map[string]struct{}
}

// NewAllScopePolicy creates a policy for system-owned or globally scoped
// resources. It accepts the same explicit system-permission option used by
// OwnerColumnPolicy.
func NewAllScopePolicy(options ...OwnerColumnOption) (*AllScopePolicy, error) {
	configuration := &OwnerColumnPolicy{
		systemPermissions: make(map[string]struct{}),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil all-scope policy option", ErrInvalidPolicy)
		}
		if err := option(configuration); err != nil {
			return nil, err
		}
	}
	return &AllScopePolicy{
		systemPermissions: configuration.systemPermissions,
	}, nil
}

// AllowsSystem reports whether this policy explicitly permits a system actor
// to perform the operation.
func (policy *AllScopePolicy) AllowsSystem(permission string) bool {
	if policy == nil {
		return false
	}
	_, ok := policy.systemPermissions[permission]
	return ok
}

// Admit requires an explicit all-scope grant for a human actor.
func (policy *AllScopePolicy) Admit(
	_ context.Context,
	actor Actor,
	permission string,
) (GrantScope, error) {
	if !actor.authenticated() {
		return "", ErrUnauthenticated
	}
	if actor.Kind == ActorKindSystem {
		if policy.AllowsSystem(permission) {
			return ScopeAll, nil
		}
		return "", fmt.Errorf(
			"%w: system permission %s is not allowed by policy",
			ErrForbidden,
			permission,
		)
	}
	scope, ok := actor.grantScope(permission)
	if !ok {
		return "", fmt.Errorf("%w: actor lacks %s", ErrForbidden, permission)
	}
	if scope != ScopeAll {
		return "", fmt.Errorf(
			"%w: permission %s requires all scope",
			ErrForbidden,
			permission,
		)
	}
	return scope, nil
}

// Check allows an authenticated user only when authorization resolved an
// all-scope grant. System callers must be explicitly admitted.
func (policy *AllScopePolicy) Check(
	_ context.Context,
	actor Actor,
	scope GrantScope,
	permission string,
	_ ResourceRef,
) error {
	if !actor.authenticated() {
		return ErrUnauthenticated
	}
	if actor.Kind == ActorKindSystem {
		if policy.AllowsSystem(permission) {
			return nil
		}
		return fmt.Errorf("%w: system permission %s is not allowed by policy", ErrForbidden, permission)
	}
	if scope != ScopeAll {
		return fmt.Errorf("%w: permission %s requires all scope", ErrForbidden, permission)
	}
	return nil
}

// Scope leaves the query unchanged only for an authorized all-scope actor.
func (policy *AllScopePolicy) Scope(
	ctx context.Context,
	actor Actor,
	scope GrantScope,
	permission string,
	query *gorm.DB,
) (*gorm.DB, error) {
	if query == nil {
		return nil, fmt.Errorf("%w: nil GORM query", ErrInvalidPolicy)
	}
	if err := policy.Check(ctx, actor, scope, permission, ResourceRef{}); err != nil {
		return nil, err
	}
	return query, nil
}
