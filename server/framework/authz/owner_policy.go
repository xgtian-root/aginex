package authz

import (
	"context"
	"fmt"
	"regexp"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var columnNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// OwnerColumnPolicy authorizes owned/all access and adds an owner predicate to
// GORM queries for own-scoped grants.
type OwnerColumnPolicy struct {
	ownerColumn       string
	systemPermissions map[string]struct{}
}

// OwnerColumnOption configures an OwnerColumnPolicy.
type OwnerColumnOption func(*OwnerColumnPolicy) error

// WithSystemPermissions explicitly allows internal system actors to perform
// the listed resource:action operations. Without this option, system actors
// are denied.
func WithSystemPermissions(permissions ...string) OwnerColumnOption {
	return func(policy *OwnerColumnPolicy) error {
		for _, permission := range permissions {
			if !permissionPattern.MatchString(permission) {
				return fmt.Errorf("%w: invalid system permission %q", ErrInvalidPolicy, permission)
			}
			policy.systemPermissions[permission] = struct{}{}
		}
		return nil
	}
}

// NewOwnerColumnPolicy creates a policy using an unqualified database column
// name such as owner_id. The column is emitted as a quoted GORM identifier.
func NewOwnerColumnPolicy(ownerColumn string, options ...OwnerColumnOption) (*OwnerColumnPolicy, error) {
	if !columnNamePattern.MatchString(ownerColumn) {
		return nil, fmt.Errorf("%w: invalid owner column %q", ErrInvalidPolicy, ownerColumn)
	}
	policy := &OwnerColumnPolicy{
		ownerColumn:       ownerColumn,
		systemPermissions: make(map[string]struct{}),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil owner policy option", ErrInvalidPolicy)
		}
		if err := option(policy); err != nil {
			return nil, err
		}
	}
	return policy, nil
}

// AllowsSystem reports whether this policy explicitly permits a system actor
// to perform the operation.
func (p *OwnerColumnPolicy) AllowsSystem(permission string) bool {
	if p == nil {
		return false
	}
	_, ok := p.systemPermissions[permission]
	return ok
}

// Admit accepts own or all grants for an owner-scoped resource. Object and
// query methods apply the corresponding ownership constraint afterwards.
func (p *OwnerColumnPolicy) Admit(
	_ context.Context,
	actor Actor,
	permission string,
) (GrantScope, error) {
	if !actor.authenticated() {
		return "", ErrUnauthenticated
	}
	if actor.Kind == ActorKindSystem {
		if p.AllowsSystem(permission) {
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
	return scope, nil
}

// Check authorizes a single owned resource.
func (p *OwnerColumnPolicy) Check(
	_ context.Context,
	actor Actor,
	scope GrantScope,
	permission string,
	resource ResourceRef,
) error {
	if !actor.authenticated() {
		return ErrUnauthenticated
	}
	if actor.Kind == ActorKindSystem {
		if p.AllowsSystem(permission) {
			return nil
		}
		return fmt.Errorf("%w: system permission %s is not allowed by policy", ErrForbidden, permission)
	}

	switch scope {
	case ScopeAll:
		return nil
	case ScopeOwn:
		if resource.OwnerID != "" && resource.OwnerID == actor.ID {
			return nil
		}
		return fmt.Errorf("%w: actor does not own %s %s", ErrForbidden, resource.Resource, resource.ID)
	default:
		return fmt.Errorf("%w: unsupported grant scope %q", ErrForbidden, scope)
	}
}

// Scope applies an owner-column predicate for own-scoped user grants.
func (p *OwnerColumnPolicy) Scope(
	_ context.Context,
	actor Actor,
	scope GrantScope,
	permission string,
	query *gorm.DB,
) (*gorm.DB, error) {
	if !actor.authenticated() {
		return nil, ErrUnauthenticated
	}
	if query == nil {
		return nil, fmt.Errorf("%w: nil GORM query", ErrInvalidPolicy)
	}
	if actor.Kind == ActorKindSystem {
		if p.AllowsSystem(permission) {
			return query, nil
		}
		return nil, fmt.Errorf("%w: system permission %s is not allowed by policy", ErrForbidden, permission)
	}

	switch scope {
	case ScopeAll:
		return query, nil
	case ScopeOwn:
		return query.Where(clause.Eq{
			Column: clause.Column{Table: clause.CurrentTable, Name: p.ownerColumn},
			Value:  actor.ID,
		}), nil
	default:
		return nil, fmt.Errorf("%w: unsupported grant scope %q", ErrForbidden, scope)
	}
}
