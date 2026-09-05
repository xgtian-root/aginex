package module

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/xgtian-root/aginex/backend/framework/authz"
)

type requestAuthorizationContextKey struct{}
type requestAuthorizationModeContextKey struct{}

// RequestAuthorization is the already-admitted authorization decision passed
// to every protected module handler. Owner/custom handlers must use Scope for
// list/count queries and Check for object operations before reading or writing
// the resource.
//
// The executable policy is deliberately private so handlers cannot replace it
// after runtime composition.
type RequestAuthorization struct {
	actor      authz.Actor
	permission string
	scope      authz.GrantScope
	policy     authz.Policy
	usage      *authorizationUsage
}

type authorizationUsage struct {
	object         atomic.Bool
	query          atomic.Bool
	queryViolation atomic.Bool
}

// NewRequestAuthorization validates and captures one admitted policy decision.
// Runtime adapters create this only after authentication, permission checking,
// and Policy.Admit have succeeded.
func NewRequestAuthorization(
	actor authz.Actor,
	permission string,
	scope authz.GrantScope,
	policy authz.Policy,
) (RequestAuthorization, error) {
	permission = strings.TrimSpace(permission)
	if !validAuthenticatedActor(actor) ||
		!validPermissionCode(permission) ||
		(scope != authz.ScopeOwn && scope != authz.ScopeAll) ||
		policyIsNil(policy) {
		return RequestAuthorization{}, fmt.Errorf(
			"%w: invalid request authorization",
			ErrInvalid,
		)
	}
	actor.Grants = append([]authz.Grant(nil), actor.Grants...)
	return RequestAuthorization{
		actor:      actor,
		permission: permission,
		scope:      scope,
		policy:     policy,
		usage:      &authorizationUsage{},
	}, nil
}

// Actor returns an immutable copy of the authenticated principal.
func (decision RequestAuthorization) Actor() authz.Actor {
	actor := decision.actor
	actor.Grants = append([]authz.Grant(nil), actor.Grants...)
	return actor
}

// Permission returns the operation permission admitted by the policy.
func (decision RequestAuthorization) Permission() string {
	return decision.permission
}

// GrantScope returns the admitted own/all scope.
func (decision RequestAuthorization) GrantScope() authz.GrantScope {
	return decision.scope
}

// Check applies the registered policy to one concrete resource.
func (decision RequestAuthorization) Check(
	ctx context.Context,
	resource authz.ResourceRef,
) error {
	if ctx == nil || policyIsNil(decision.policy) {
		return fmt.Errorf("%w: invalid request authorization", ErrInvalid)
	}
	err := decision.policy.Check(
		ctx,
		decision.actor,
		decision.scope,
		decision.permission,
		resource,
	)
	if err == nil && decision.usage != nil {
		decision.usage.object.Store(true)
	}
	return err
}

// Scope applies the registered policy to an opaque read-only query before
// list/count/object execution. The returned query retains its policy across
// every supported builder operation and exposes no GORM session/raw escape.
func (decision RequestAuthorization) Scope(
	ctx context.Context,
	query Query,
) (Query, error) {
	if ctx == nil || query.db == nil || policyIsNil(decision.policy) {
		return Query{}, fmt.Errorf(
			"%w: invalid request authorization",
			ErrInvalid,
		)
	}
	if query.guarded &&
		query.authorization.usage != decision.usage {
		return Query{}, fmt.Errorf(
			"%w: query belongs to another authorization decision",
			ErrInvalid,
		)
	}
	query.authorization = decision
	query.guarded = true
	query.scoped = true
	query.scopeContext = ctx
	return query, nil
}

// Satisfies reports whether a successful response may be committed for the
// route's declared authorization mode. It exposes no mutable policy state.
func (decision RequestAuthorization) Satisfies(
	mode AuthorizationMode,
) bool {
	if decision.Violated() {
		return false
	}
	switch mode {
	case AuthorizationAll:
		return decision.scope == authz.ScopeAll
	case AuthorizationActor:
		return validAuthenticatedActor(decision.actor)
	case AuthorizationObject:
		return decision.usage != nil &&
			(decision.usage.object.Load() ||
				decision.usage.query.Load())
	case AuthorizationQuery:
		return decision.usage != nil &&
			decision.usage.query.Load() &&
			!decision.usage.queryViolation.Load()
	default:
		return false
	}
}

// ContextWithRequestAuthorizationMode records the route's required
// authorization consumption before module middleware or handlers run.
func ContextWithRequestAuthorizationMode(
	ctx context.Context,
	mode AuthorizationMode,
) (context.Context, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: invalid request context", ErrInvalid)
	}
	switch mode {
	case AuthorizationAll, AuthorizationActor,
		AuthorizationObject, AuthorizationQuery:
	default:
		return nil, fmt.Errorf(
			"%w: invalid authorization mode %q",
			ErrInvalid,
			mode,
		)
	}
	return context.WithValue(
		ctx,
		requestAuthorizationModeContextKey{},
		mode,
	), nil
}

func requestAuthorizationModeFromContext(
	ctx context.Context,
) (AuthorizationMode, bool) {
	if ctx == nil {
		return "", false
	}
	mode, ok := ctx.Value(
		requestAuthorizationModeContextKey{},
	).(AuthorizationMode)
	return mode, ok
}

// Violated reports whether a query protected by this decision attempted to
// execute through an unscoped module database handle. Once observed, the
// violation is permanent for the request and every response must fail closed.
func (decision RequestAuthorization) Violated() bool {
	return decision.usage != nil &&
		decision.usage.queryViolation.Load()
}

// ContextWithRequestAuthorization attaches an admitted decision to a request
// context. Passing an invalid zero decision is rejected.
func ContextWithRequestAuthorization(
	ctx context.Context,
	decision RequestAuthorization,
) (context.Context, error) {
	if ctx == nil || policyIsNil(decision.policy) || decision.usage == nil {
		return nil, fmt.Errorf("%w: invalid request authorization", ErrInvalid)
	}
	return context.WithValue(
		ctx,
		requestAuthorizationContextKey{},
		decision,
	), nil
}

// RequestAuthorizationFromContext returns the admitted decision for a
// protected module request.
func RequestAuthorizationFromContext(
	ctx context.Context,
) (RequestAuthorization, bool) {
	if ctx == nil {
		return RequestAuthorization{}, false
	}
	decision, ok := ctx.Value(
		requestAuthorizationContextKey{},
	).(RequestAuthorization)
	return decision, ok &&
		!policyIsNil(decision.policy) &&
		decision.usage != nil
}
