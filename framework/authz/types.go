// Package authz provides the framework authorization contracts shared by HTTP
// handlers, services, and background workers.
package authz

import (
	"errors"
	"strings"
)

var (
	// ErrUnauthenticated means no valid user or system actor was supplied.
	ErrUnauthenticated = errors.New("authz: unauthenticated")
	// ErrForbidden means an authenticated actor was not explicitly authorized.
	ErrForbidden = errors.New("authz: forbidden")
	// ErrInvalidPolicy means authorization was configured incorrectly.
	ErrInvalidPolicy = errors.New("authz: invalid policy")
)

// ActorKind identifies the kind of principal making a request.
type ActorKind string

const (
	// ActorKindUser is an authenticated human user.
	ActorKindUser ActorKind = "user"
	// ActorKindSystem is a trusted internal process. A system actor has no
	// implicit bypass and must be explicitly admitted by the resource policy.
	ActorKindSystem ActorKind = "system"
)

// GrantScope limits a permission grant to owned resources or all resources.
type GrantScope string

const (
	// ScopeOwn permits access only to resources owned by the actor.
	ScopeOwn GrantScope = "own"
	// ScopeAll permits access to every resource covered by the policy.
	ScopeAll GrantScope = "all"
)

// Grant associates a lowercase resource:action permission with a scope.
type Grant struct {
	Permission string
	Scope      GrantScope
}

// Actor is the authenticated principal used for authorization decisions.
type Actor struct {
	Kind   ActorKind
	ID     string
	Grants []Grant
}

// NewUserActor creates a user actor with the supplied permission grants.
func NewUserActor(id string, grants ...Grant) Actor {
	return Actor{Kind: ActorKindUser, ID: strings.TrimSpace(id), Grants: grants}
}

// NewSystemActor creates an internal actor. System access is still denied
// unless the registered policy explicitly allows the requested permission.
func NewSystemActor(id string) Actor {
	return Actor{Kind: ActorKindSystem, ID: strings.TrimSpace(id)}
}

func (a Actor) authenticated() bool {
	if strings.TrimSpace(a.ID) == "" {
		return false
	}
	return a.Kind == ActorKindUser || a.Kind == ActorKindSystem
}

func (a Actor) grantScope(permission string) (GrantScope, bool) {
	var own bool
	for _, grant := range a.Grants {
		if grant.Permission != permission {
			continue
		}
		switch grant.Scope {
		case ScopeAll:
			return ScopeAll, true
		case ScopeOwn:
			own = true
		}
	}
	if own {
		return ScopeOwn, true
	}
	return "", false
}

// ResourceRef identifies a resource instance and its owner for an object-level
// authorization decision.
type ResourceRef struct {
	Resource string
	ID       string
	OwnerID  string
}
