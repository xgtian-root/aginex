package authz

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"

	"gorm.io/gorm"
)

var (
	resourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	permissionPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$`)
)

// Policy makes object and query-scope decisions for one registered resource.
// Implementations must deny access unless a branch explicitly allows it.
type Policy interface {
	Admit(context.Context, Actor, string) (GrantScope, error)
	Check(context.Context, Actor, GrantScope, string, ResourceRef) error
	Scope(context.Context, Actor, GrantScope, string, *gorm.DB) (*gorm.DB, error)
	AllowsSystem(permission string) bool
}

// Authorizer dispatches authorization decisions to explicitly registered
// resource policies. Its zero value is usable and denies every request.
type Authorizer struct {
	mu       sync.RWMutex
	policies map[string]Policy
}

// NewAuthorizer creates an empty, deny-by-default authorizer.
func NewAuthorizer() *Authorizer {
	return &Authorizer{policies: make(map[string]Policy)}
}

// Register associates one resource name with a policy. Replacing a registered
// policy is rejected so module order cannot silently weaken authorization.
func (a *Authorizer) Register(resource string, policy Policy) error {
	if a == nil {
		return fmt.Errorf("%w: nil authorizer", ErrInvalidPolicy)
	}
	if !resourceNamePattern.MatchString(resource) {
		return fmt.Errorf("%w: invalid resource %q", ErrInvalidPolicy, resource)
	}
	if policyIsNil(policy) {
		return fmt.Errorf("%w: nil policy for %s", ErrInvalidPolicy, resource)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.policies == nil {
		a.policies = make(map[string]Policy)
	}
	if _, exists := a.policies[resource]; exists {
		return fmt.Errorf("%w: policy already registered for %s", ErrInvalidPolicy, resource)
	}
	a.policies[resource] = policy
	return nil
}

// Check authorizes an operation on a specific resource instance.
func (a *Authorizer) Check(
	ctx context.Context,
	actor Actor,
	permission string,
	resource ResourceRef,
) error {
	if !actor.authenticated() {
		return ErrUnauthenticated
	}
	if !permissionMatchesResource(permission, resource.Resource) {
		return fmt.Errorf("%w: invalid permission or resource", ErrForbidden)
	}
	policy, ok := a.registeredPolicy(resource.Resource)
	if !ok {
		return fmt.Errorf("%w: no policy registered for %s", ErrForbidden, resource.Resource)
	}

	scope, err := policy.Admit(ctx, actor, permission)
	if err != nil {
		return err
	}
	return policy.Check(ctx, actor, scope, permission, resource)
}

// Scope applies the registered policy to a GORM query before it is executed.
// Callers must use the returned query for both list results and counts.
func (a *Authorizer) Scope(
	ctx context.Context,
	actor Actor,
	permission string,
	resource string,
	query *gorm.DB,
) (*gorm.DB, error) {
	if !actor.authenticated() {
		return nil, ErrUnauthenticated
	}
	if query == nil {
		return nil, fmt.Errorf("%w: nil GORM query", ErrInvalidPolicy)
	}
	if !permissionMatchesResource(permission, resource) {
		return nil, fmt.Errorf("%w: invalid permission or resource", ErrForbidden)
	}
	policy, ok := a.registeredPolicy(resource)
	if !ok {
		return nil, fmt.Errorf("%w: no policy registered for %s", ErrForbidden, resource)
	}

	scope, err := policy.Admit(ctx, actor, permission)
	if err != nil {
		return nil, err
	}
	scoped, err := policy.Scope(ctx, actor, scope, permission, query)
	if err != nil {
		return nil, err
	}
	if scoped == nil {
		return nil, fmt.Errorf("%w: policy returned a nil GORM query", ErrInvalidPolicy)
	}
	return scoped, nil
}

func (a *Authorizer) registeredPolicy(resource string) (Policy, bool) {
	if a == nil {
		return nil, false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	policy, ok := a.policies[resource]
	return policy, ok && !policyIsNil(policy)
}

func permissionMatchesResource(permission, resource string) bool {
	if !resourceNamePattern.MatchString(resource) || !permissionPattern.MatchString(permission) {
		return false
	}
	namespace, _, _ := strings.Cut(permission, ":")
	return namespace == resource
}

func policyIsNil(policy Policy) bool {
	if policy == nil {
		return true
	}
	value := reflect.ValueOf(policy)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
