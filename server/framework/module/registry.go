package module

import (
	"fmt"
	"math"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/framework/ratelimit"
)

var (
	namePattern               = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	permissionPattern         = regexp.MustCompile(`^[a-z][a-z0-9_-]*:[a-z][a-z0-9_-]*$`)
	operationIDPattern        = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	rateLimitNamespacePattern = regexp.MustCompile(
		`^[a-z][a-z0-9._:-]*$`,
	)
	apiLiteralPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*$`)
	apiParameterPattern = regexp.MustCompile(
		`^\{([A-Za-z][A-Za-z0-9_-]*)(\+)?\}$`,
	)
)

// Registry accumulates module metadata. Registration is deliberately serial;
// callers should finish composition before sharing snapshots with runtimes.
type Registry struct {
	modules         map[string]struct{}
	permissions     map[string]PermissionDefinition
	operations      map[string]OperationDefinition
	operationRoutes map[string]string
	httpRoutes      map[string]HTTPRoute
	apiContracts    map[string]APIContract
	authenticators  map[AuthenticationRef]AuthenticationScheme
	policies        map[PolicyRef]authz.Policy
	resources       map[string]ResourceDefinition
	readiness       map[string]ReadinessCheck
	migrations      map[string]MigrationBundle
	jobs            map[string]JobHandlerDefinition
	lifecycle       map[string]LifecycleHook
	registrationErr error
	collectErrors   bool
}

// NewRegistry returns an empty module registry.
func NewRegistry() *Registry {
	registry := &Registry{}
	registry.ensureInitialized()
	return registry
}

// RegisterModules validates module names up front, invokes modules in name
// order, and commits all metadata atomically only after final validation.
func (r *Registry) RegisterModules(modules ...Module) error {
	r.ensureInitialized()
	if r.registrationErr != nil {
		return r.registrationErr
	}

	type namedModule struct {
		name   string
		module Module
	}
	ordered := make([]namedModule, 0, len(modules))
	seen := make(map[string]struct{}, len(r.modules)+len(modules))
	for name := range r.modules {
		seen[name] = struct{}{}
	}
	for _, candidate := range modules {
		if isNilModule(candidate) {
			return fmt.Errorf("%w: nil module", ErrInvalid)
		}
		name := strings.TrimSpace(candidate.Name())
		if !validName(name) || name != candidate.Name() {
			return fmt.Errorf("%w: module name %q", ErrInvalid, candidate.Name())
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: module %q", ErrDuplicate, name)
		}
		seen[name] = struct{}{}
		ordered = append(ordered, namedModule{name: name, module: candidate})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].name < ordered[j].name
	})

	staged := r.clone()
	staged.collectErrors = true
	for _, item := range ordered {
		staged.modules[item.name] = struct{}{}
		if err := item.module.Register(staged); err != nil {
			return fmt.Errorf("register module %q: %w", item.name, err)
		}
		if staged.registrationErr != nil {
			return fmt.Errorf("register module %q: %w", item.name, staged.registrationErr)
		}
	}
	staged.collectErrors = false
	if err := staged.Validate(); err != nil {
		return err
	}
	*r = *staged
	return nil
}

// RegisterPermission adds one permission definition.
func (r *Registry) RegisterPermission(permission PermissionDefinition) error {
	r.ensureInitialized()
	permission.Code = strings.TrimSpace(permission.Code)
	permission.Description = strings.TrimSpace(permission.Description)
	if !validPermissionCode(permission.Code) || permission.Description == "" {
		return r.fail(fmt.Errorf("%w: permission %q", ErrInvalid, permission.Code))
	}
	if _, exists := r.permissions[permission.Code]; exists {
		return r.fail(fmt.Errorf("%w: permission %q", ErrDuplicate, permission.Code))
	}
	r.permissions[permission.Code] = permission
	return nil
}

// RegisterOperation adds one API operation description.
func (r *Registry) RegisterOperation(operation OperationDefinition) error {
	r.ensureInitialized()
	operation.ID = strings.TrimSpace(operation.ID)
	operation.Path = strings.TrimSpace(operation.Path)
	operation.Authentication = AuthenticationRef(
		strings.TrimSpace(string(operation.Authentication)),
	)
	operation.Permission = strings.TrimSpace(operation.Permission)
	operation.Policy = PolicyRef(strings.TrimSpace(string(operation.Policy)))
	operation.Idempotency = IdempotencyPolicy(
		strings.TrimSpace(string(operation.Idempotency)),
	)
	operation.RateLimit.Namespace = strings.TrimSpace(
		operation.RateLimit.Namespace,
	)
	operation.RateLimit.Subject = RateLimitSubject(
		strings.TrimSpace(string(operation.RateLimit.Subject)),
	)
	if err := validateOperation(operation); err != nil {
		return r.fail(err)
	}
	if _, exists := r.operations[operation.ID]; exists {
		return r.fail(fmt.Errorf("%w: operation id %q", ErrDuplicate, operation.ID))
	}
	routeKey := string(operation.Method) + " " + canonicalRouteShape(operation.Path)
	if existing, exists := r.operationRoutes[routeKey]; exists {
		return r.fail(fmt.Errorf("%w: route %s already belongs to operation %q", ErrDuplicate, routeKey, existing))
	}
	r.operations[operation.ID] = operation
	r.operationRoutes[routeKey] = operation.ID
	return nil
}

// RegisterHTTPRoute binds one operation to its runtime Gin handler and
// operation-local middleware. The operation may be registered before or after
// this binding; cross-reference validation runs after module composition.
func (r *Registry) RegisterHTTPRoute(route HTTPRoute) error {
	r.ensureInitialized()
	route.OperationID = strings.TrimSpace(route.OperationID)
	if err := validateHTTPRoute(route); err != nil {
		return r.fail(err)
	}
	if _, exists := r.httpRoutes[route.OperationID]; exists {
		return r.fail(fmt.Errorf("%w: HTTP route for operation %q", ErrDuplicate, route.OperationID))
	}
	r.httpRoutes[route.OperationID] = cloneHTTPRoute(route)
	return nil
}

// RegisterAPIContract adds the typed OpenAPI source for one operation.
func (r *Registry) RegisterAPIContract(contract APIContract) error {
	r.ensureInitialized()
	contract.OperationID = strings.TrimSpace(contract.OperationID)
	contract.Summary = strings.TrimSpace(contract.Summary)
	contract.Tag = strings.TrimSpace(contract.Tag)
	contract.RequestMediaType = strings.TrimSpace(contract.RequestMediaType)
	contract.ResponseMediaType = strings.TrimSpace(contract.ResponseMediaType)
	if err := validateAPIContract(contract); err != nil {
		return r.fail(err)
	}
	if _, exists := r.apiContracts[contract.OperationID]; exists {
		return r.fail(fmt.Errorf("%w: API contract for operation %q", ErrDuplicate, contract.OperationID))
	}
	r.apiContracts[contract.OperationID] = cloneAPIContract(contract)
	return nil
}

// RegisterAuthorizationPolicy associates a protected-operation PolicyRef with
// an executable fail-closed object/query policy.
func (r *Registry) RegisterAuthorizationPolicy(definition AuthorizationPolicy) error {
	r.ensureInitialized()
	definition.Ref = PolicyRef(strings.TrimSpace(string(definition.Ref)))
	if err := validateAuthorizationPolicy(definition); err != nil {
		return r.fail(err)
	}
	if _, exists := r.policies[definition.Ref]; exists {
		return r.fail(fmt.Errorf("%w: authorization policy %q", ErrDuplicate, definition.Ref))
	}
	r.policies[definition.Ref] = definition.Policy
	return nil
}

// RegisterAuthenticationScheme adds one executable authentication middleware
// and its matching OpenAPI security definition.
func (r *Registry) RegisterAuthenticationScheme(
	definition AuthenticationScheme,
) error {
	r.ensureInitialized()
	definition.Ref = AuthenticationRef(
		strings.TrimSpace(string(definition.Ref)),
	)
	definition.CookieName = strings.TrimSpace(definition.CookieName)
	definition.BearerFormat = strings.TrimSpace(definition.BearerFormat)
	definition.Description = strings.TrimSpace(definition.Description)
	if err := validateAuthenticationScheme(definition); err != nil {
		return r.fail(err)
	}
	if _, exists := r.authenticators[definition.Ref]; exists {
		return r.fail(fmt.Errorf(
			"%w: authentication scheme %q",
			ErrDuplicate,
			definition.Ref,
		))
	}
	r.authenticators[definition.Ref] = definition
	return nil
}

// RegisterResource adds immutable build-time resource metadata. Cross-checks
// against operation permissions and policies run after every module has
// registered, so resources may reference operations declared later.
func (r *Registry) RegisterResource(resource ResourceDefinition) error {
	r.ensureInitialized()
	normalized, err := validateResource(resource)
	if err != nil {
		return r.fail(err)
	}
	if _, exists := r.resources[normalized.Name]; exists {
		return r.fail(fmt.Errorf(
			"%w: resource %q",
			ErrDuplicate,
			normalized.Name,
		))
	}
	r.resources[normalized.Name] = cloneResource(normalized)
	return nil
}

// RegisterReadinessCheck adds one bounded external dependency probe. Checks
// are executed only by readiness entrypoints, never during registration.
func (r *Registry) RegisterReadinessCheck(check ReadinessCheck) error {
	r.ensureInitialized()
	check.Name = strings.TrimSpace(check.Name)
	if err := validateReadinessCheck(check); err != nil {
		return r.fail(err)
	}
	if _, exists := r.readiness[check.Name]; exists {
		return r.fail(fmt.Errorf(
			"%w: readiness check %q",
			ErrDuplicate,
			check.Name,
		))
	}
	r.readiness[check.Name] = check
	return nil
}

// RegisterMigrationBundle adds an immutable migration bundle description.
func (r *Registry) RegisterMigrationBundle(bundle MigrationBundle) error {
	r.ensureInitialized()
	if err := validateMigrationBundle(bundle); err != nil {
		return r.fail(err)
	}
	if _, exists := r.migrations[bundle.Name()]; exists {
		return r.fail(fmt.Errorf("%w: migration bundle %q", ErrDuplicate, bundle.Name()))
	}
	r.migrations[bundle.Name()] = bundle.clone()
	return nil
}

// RegisterJobHandler adds a versioned job handler description.
func (r *Registry) RegisterJobHandler(handler JobHandlerDefinition) error {
	r.ensureInitialized()
	handler.Type = strings.TrimSpace(handler.Type)
	if !validName(handler.Type) || handler.Version == 0 || handler.Handle == nil {
		return r.fail(fmt.Errorf("%w: job handler %q version %d", ErrInvalid, handler.Type, handler.Version))
	}
	key := jobKey(handler.Type, handler.Version)
	if _, exists := r.jobs[key]; exists {
		return r.fail(fmt.Errorf("%w: job handler %s", ErrDuplicate, key))
	}
	r.jobs[key] = handler
	return nil
}

// RegisterLifecycleHook adds optional lifecycle callbacks without invoking them.
func (r *Registry) RegisterLifecycleHook(hook LifecycleHook) error {
	r.ensureInitialized()
	hook.Name = strings.TrimSpace(hook.Name)
	if !validName(hook.Name) || (hook.Start == nil && hook.Stop == nil) {
		return r.fail(fmt.Errorf("%w: lifecycle hook %q", ErrInvalid, hook.Name))
	}
	if _, exists := r.lifecycle[hook.Name]; exists {
		return r.fail(fmt.Errorf("%w: lifecycle hook %q", ErrDuplicate, hook.Name))
	}
	r.lifecycle[hook.Name] = hook
	return nil
}

// Validate checks cross-definition references after all modules are registered.
func (r *Registry) Validate() error {
	r.ensureInitialized()
	if r.registrationErr != nil {
		return r.registrationErr
	}
	for _, operation := range r.operations {
		if operation.Public {
			if _, exists := r.policies[operation.Policy]; operation.Policy != "" && exists {
				return fmt.Errorf("%w: public operation %q references authorization policy", ErrInvalid, operation.ID)
			}
		} else {
			if _, exists := r.permissions[operation.Permission]; !exists {
				return fmt.Errorf("%w: operation %q references unregistered permission %q", ErrInvalid, operation.ID, operation.Permission)
			}
		}
	}
	for operationID := range r.httpRoutes {
		if _, exists := r.operations[operationID]; !exists {
			return fmt.Errorf("%w: HTTP route references unregistered operation %q", ErrInvalid, operationID)
		}
	}
	for operationID, contract := range r.apiContracts {
		operation, exists := r.operations[operationID]
		if !exists {
			return fmt.Errorf("%w: API contract references unregistered operation %q", ErrInvalid, operationID)
		}
		if operation.Method == MethodGet || operation.Method == MethodHead {
			if contract.RequestDTO != nil {
				return fmt.Errorf("%w: read operation %q declares a request body", ErrInvalid, operationID)
			}
		}
		for _, parameter := range contract.Parameters {
			if parameter.In == ParameterHeader &&
				strings.EqualFold(parameter.Name, "Idempotency-Key") {
				return fmt.Errorf(
					"%w: operation %q contract must derive Idempotency-Key from its operation policy",
					ErrInvalid,
					operationID,
				)
			}
			if parameter.In == ParameterPath && !operationHasPathParameter(operation.Path, parameter.Name) {
				return fmt.Errorf(
					"%w: operation %q contract references missing path parameter %q",
					ErrInvalid,
					operationID,
					parameter.Name,
				)
			}
		}
	}
	for _, resource := range r.resources {
		for _, reference := range resource.Operations {
			operation, exists := r.operations[reference.OperationID]
			if !exists {
				return fmt.Errorf(
					"%w: resource %q references unregistered operation %q",
					ErrInvalid,
					resource.Name,
					reference.OperationID,
				)
			}
			if operation.Public ||
				operation.Policy != resource.Policy ||
				!strings.HasPrefix(operation.Permission, resource.Name+":") {
				return fmt.Errorf(
					"%w: resource %q operation %q has inconsistent authorization",
					ErrInvalid,
					resource.Name,
					reference.OperationID,
				)
			}
		}
	}
	return nil
}

// ValidateRuntime is the production composition gate. Every external
// operation must have exactly one handler and typed API contract, and every
// protected operation must resolve to an executable authorization policy.
func (r *Registry) ValidateRuntime() error {
	if err := r.Validate(); err != nil {
		return err
	}
	for _, operation := range r.operations {
		route, exists := r.httpRoutes[operation.ID]
		if !exists {
			return fmt.Errorf("%w: operation %q has no HTTP route", ErrInvalid, operation.ID)
		}
		if operation.Public {
			if route.Handler == nil ||
				route.AuthorizedHandler != nil ||
				route.Authorization != "" ||
				route.ReplayAuthorizer != nil {
				return fmt.Errorf(
					"%w: public operation %q must register a public handler without authorization mode",
					ErrInvalid,
					operation.ID,
				)
			}
		} else {
			if route.Handler != nil ||
				route.AuthorizedHandler == nil ||
				route.Authorization == "" {
				return fmt.Errorf(
					"%w: protected operation %q must register an authorized handler and mode",
					ErrInvalid,
					operation.ID,
				)
			}
		}
		replayRequiresScope := operation.Idempotency.Enabled() &&
			(route.Authorization == AuthorizationObject ||
				route.Authorization == AuthorizationQuery)
		if replayRequiresScope && route.ReplayAuthorizer == nil {
			return fmt.Errorf(
				"%w: scoped idempotent operation %q must register a replay authorizer",
				ErrInvalid,
				operation.ID,
			)
		}
		if route.ReplayAuthorizer != nil && !replayRequiresScope {
			return fmt.Errorf(
				"%w: operation %q registers an inapplicable replay authorizer",
				ErrInvalid,
				operation.ID,
			)
		}
		contract, exists := r.apiContracts[operation.ID]
		if !exists {
			return fmt.Errorf("%w: operation %q has no API contract", ErrInvalid, operation.ID)
		}
		if !operation.Public {
			if _, exists := r.authenticators[operation.Authentication]; !exists {
				return fmt.Errorf(
					"%w: operation %q references unregistered authentication scheme %q",
					ErrInvalid,
					operation.ID,
					operation.Authentication,
				)
			}
			if _, exists := r.policies[operation.Policy]; !exists {
				return fmt.Errorf(
					"%w: operation %q references unregistered policy %q",
					ErrInvalid,
					operation.ID,
					operation.Policy,
				)
			}
			if !contractHasErrorStatus(contract, 401) ||
				!contractHasErrorStatus(contract, 403) {
				return fmt.Errorf(
					"%w: protected operation %q must document 401 and 403 errors",
					ErrInvalid,
					operation.ID,
				)
			}
		}
	}
	for _, resource := range r.resources {
		for _, reference := range resource.Operations {
			route := r.httpRoutes[reference.OperationID]
			if (resource.Ownership == OwnershipOwner ||
				resource.Ownership == OwnershipCustom) &&
				route.Authorization != AuthorizationObject &&
				route.Authorization != AuthorizationQuery {
				return fmt.Errorf(
					"%w: scoped resource %q operation %q must use object or query authorization",
					ErrInvalid,
					resource.Name,
					reference.OperationID,
				)
			}
			contract, exists := r.apiContracts[reference.OperationID]
			if !exists {
				return fmt.Errorf(
					"%w: resource %q operation %q has no API contract",
					ErrInvalid,
					resource.Name,
					reference.OperationID,
				)
			}
			if !sameDTOType(reference.RequestDTO, contract.RequestDTO) ||
				!sameDTOType(reference.ResponseDTO, contract.ResponseDTO) {
				return fmt.Errorf(
					"%w: resource %q operation %q DTOs drift from its API contract",
					ErrInvalid,
					resource.Name,
					reference.OperationID,
				)
			}
		}
	}
	return nil
}

// Modules returns registered module names in deterministic order.
func (r *Registry) Modules() []string {
	r.ensureInitialized()
	result := make([]string, 0, len(r.modules))
	for name := range r.modules {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

// Permissions returns copied permission definitions ordered by code.
func (r *Registry) Permissions() []PermissionDefinition {
	r.ensureInitialized()
	result := make([]PermissionDefinition, 0, len(r.permissions))
	for _, permission := range r.permissions {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Code < result[j].Code
	})
	return result
}

// Operations returns copied operation definitions ordered by operation ID.
func (r *Registry) Operations() []OperationDefinition {
	r.ensureInitialized()
	result := make([]OperationDefinition, 0, len(r.operations))
	for _, operation := range r.operations {
		result = append(result, operation)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})
	return result
}

// HTTPRoutes returns copied route bindings ordered by operation ID.
func (r *Registry) HTTPRoutes() []HTTPRoute {
	r.ensureInitialized()
	result := make([]HTTPRoute, 0, len(r.httpRoutes))
	for _, route := range r.httpRoutes {
		result = append(result, cloneHTTPRoute(route))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].OperationID < result[j].OperationID
	})
	return result
}

// HTTPRoute returns a copied route binding for one operation.
func (r *Registry) HTTPRoute(operationID string) (HTTPRoute, bool) {
	r.ensureInitialized()
	route, ok := r.httpRoutes[operationID]
	return cloneHTTPRoute(route), ok
}

// APIContracts returns copied typed contracts ordered by operation ID.
func (r *Registry) APIContracts() []APIContract {
	r.ensureInitialized()
	result := make([]APIContract, 0, len(r.apiContracts))
	for _, contract := range r.apiContracts {
		result = append(result, cloneAPIContract(contract))
	}
	sortAPIContracts(result)
	return result
}

// APIContract returns a copied typed contract for one operation.
func (r *Registry) APIContract(operationID string) (APIContract, bool) {
	r.ensureInitialized()
	contract, ok := r.apiContracts[operationID]
	return cloneAPIContract(contract), ok
}

// AuthorizationPolicies returns registered executable policies ordered by ref.
// Policy implementations are shared references and must be immutable.
func (r *Registry) AuthorizationPolicies() []AuthorizationPolicy {
	r.ensureInitialized()
	result := make([]AuthorizationPolicy, 0, len(r.policies))
	for ref, policy := range r.policies {
		result = append(result, AuthorizationPolicy{Ref: ref, Policy: policy})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Ref < result[j].Ref
	})
	return result
}

// AuthorizationPolicy resolves one registered PolicyRef.
func (r *Registry) AuthorizationPolicy(ref PolicyRef) (authz.Policy, bool) {
	r.ensureInitialized()
	policy, ok := r.policies[ref]
	return policy, ok
}

// AuthenticationSchemes returns registered executable schemes ordered by ref.
func (r *Registry) AuthenticationSchemes() []AuthenticationScheme {
	r.ensureInitialized()
	result := make(
		[]AuthenticationScheme,
		0,
		len(r.authenticators),
	)
	for _, definition := range r.authenticators {
		result = append(result, definition)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Ref < result[j].Ref
	})
	return result
}

// AuthenticationScheme resolves one registered authenticator.
func (r *Registry) AuthenticationScheme(
	ref AuthenticationRef,
) (AuthenticationScheme, bool) {
	r.ensureInitialized()
	definition, ok := r.authenticators[ref]
	return definition, ok
}

// Resources returns immutable resource snapshots ordered by name.
func (r *Registry) Resources() []ResourceDefinition {
	r.ensureInitialized()
	result := make([]ResourceDefinition, 0, len(r.resources))
	for _, resource := range r.resources {
		result = append(result, cloneResource(resource))
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// ReadinessChecks returns registered probes ordered by stable name.
func (r *Registry) ReadinessChecks() []ReadinessCheck {
	r.ensureInitialized()
	result := make([]ReadinessCheck, 0, len(r.readiness))
	for _, check := range r.readiness {
		result = append(result, check)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// MigrationBundles returns immutable migration bundle snapshots ordered by name.
func (r *Registry) MigrationBundles() []MigrationBundle {
	r.ensureInitialized()
	result := make([]MigrationBundle, 0, len(r.migrations))
	for _, bundle := range r.migrations {
		result = append(result, bundle.clone())
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name() < result[j].Name()
	})
	return result
}

// JobHandlers returns job handler descriptions ordered by type and version.
func (r *Registry) JobHandlers() []JobHandlerDefinition {
	r.ensureInitialized()
	result := make([]JobHandlerDefinition, 0, len(r.jobs))
	for _, handler := range r.jobs {
		result = append(result, handler)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type == result[j].Type {
			return result[i].Version < result[j].Version
		}
		return result[i].Type < result[j].Type
	})
	return result
}

// LifecycleHooks returns lifecycle descriptions ordered by name.
func (r *Registry) LifecycleHooks() []LifecycleHook {
	r.ensureInitialized()
	result := make([]LifecycleHook, 0, len(r.lifecycle))
	for _, hook := range r.lifecycle {
		result = append(result, hook)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func (r *Registry) ensureInitialized() {
	if r.modules == nil {
		r.modules = make(map[string]struct{})
	}
	if r.permissions == nil {
		r.permissions = make(map[string]PermissionDefinition)
	}
	if r.operations == nil {
		r.operations = make(map[string]OperationDefinition)
	}
	if r.operationRoutes == nil {
		r.operationRoutes = make(map[string]string)
	}
	if r.httpRoutes == nil {
		r.httpRoutes = make(map[string]HTTPRoute)
	}
	if r.apiContracts == nil {
		r.apiContracts = make(map[string]APIContract)
	}
	if r.authenticators == nil {
		r.authenticators = make(
			map[AuthenticationRef]AuthenticationScheme,
		)
	}
	if r.policies == nil {
		r.policies = make(map[PolicyRef]authz.Policy)
	}
	if r.resources == nil {
		r.resources = make(map[string]ResourceDefinition)
	}
	if r.readiness == nil {
		r.readiness = make(map[string]ReadinessCheck)
	}
	if r.migrations == nil {
		r.migrations = make(map[string]MigrationBundle)
	}
	if r.jobs == nil {
		r.jobs = make(map[string]JobHandlerDefinition)
	}
	if r.lifecycle == nil {
		r.lifecycle = make(map[string]LifecycleHook)
	}
}

func (r *Registry) fail(err error) error {
	if r.collectErrors && r.registrationErr == nil {
		r.registrationErr = err
	}
	return err
}

func (r *Registry) clone() *Registry {
	cloned := NewRegistry()
	cloned.registrationErr = r.registrationErr
	cloned.collectErrors = r.collectErrors
	for name := range r.modules {
		cloned.modules[name] = struct{}{}
	}
	for code, permission := range r.permissions {
		cloned.permissions[code] = permission
	}
	for id, operation := range r.operations {
		cloned.operations[id] = operation
	}
	for route, id := range r.operationRoutes {
		cloned.operationRoutes[route] = id
	}
	for operationID, route := range r.httpRoutes {
		cloned.httpRoutes[operationID] = cloneHTTPRoute(route)
	}
	for operationID, contract := range r.apiContracts {
		cloned.apiContracts[operationID] = cloneAPIContract(contract)
	}
	for ref, definition := range r.authenticators {
		cloned.authenticators[ref] = definition
	}
	for ref, policy := range r.policies {
		cloned.policies[ref] = policy
	}
	for name, resource := range r.resources {
		cloned.resources[name] = cloneResource(resource)
	}
	for name, check := range r.readiness {
		cloned.readiness[name] = check
	}
	for name, bundle := range r.migrations {
		cloned.migrations[name] = bundle.clone()
	}
	for key, handler := range r.jobs {
		cloned.jobs[key] = handler
	}
	for name, hook := range r.lifecycle {
		cloned.lifecycle[name] = hook
	}
	return cloned
}

func validateOperation(operation OperationDefinition) error {
	if !operationIDPattern.MatchString(operation.ID) {
		return fmt.Errorf("%w: operation id %q", ErrInvalid, operation.ID)
	}
	if !validHTTPMethod(operation.Method) {
		return fmt.Errorf("%w: operation %q has method %q", ErrInvalid, operation.ID, operation.Method)
	}
	if !validAPIPath(operation.Path) {
		return fmt.Errorf("%w: operation %q has path %q", ErrInvalid, operation.ID, operation.Path)
	}
	switch operation.Idempotency {
	case IdempotencyDisabled:
	case IdempotencyOptional:
		if operation.Public || !isWriteMethod(operation.Method) {
			return fmt.Errorf(
				"%w: operation %q idempotency requires an authenticated write method",
				ErrInvalid,
				operation.ID,
			)
		}
	default:
		return fmt.Errorf(
			"%w: operation %q has idempotency policy %q",
			ErrInvalid,
			operation.ID,
			operation.Idempotency,
		)
	}
	if err := validateRateLimitPolicy(operation); err != nil {
		return err
	}
	if operation.Public {
		if operation.Authentication != "" ||
			operation.Permission != "" ||
			operation.Policy != "" {
			return fmt.Errorf("%w: public operation %q also declares protected authorization", ErrInvalid, operation.ID)
		}
		return nil
	}
	if !operationIDPattern.MatchString(string(operation.Authentication)) ||
		!validPermissionCode(operation.Permission) ||
		!validName(string(operation.Policy)) {
		return fmt.Errorf(
			"%w: operation %q must declare authentication, permission, and policy",
			ErrInvalid,
			operation.ID,
		)
	}
	return nil
}

func validateRateLimitPolicy(operation OperationDefinition) error {
	policy := operation.RateLimit
	if !policy.Enabled() {
		return nil
	}
	if len(policy.Namespace) > ratelimit.MaxNamespaceBytes ||
		!rateLimitNamespacePattern.MatchString(policy.Namespace) {
		return fmt.Errorf(
			"%w: operation %q has invalid rate-limit namespace %q",
			ErrInvalid,
			operation.ID,
			policy.Namespace,
		)
	}
	switch policy.Subject {
	case RateLimitByIP:
	case RateLimitByActor:
		if operation.Public {
			return fmt.Errorf(
				"%w: public operation %q cannot rate limit by authenticated actor",
				ErrInvalid,
				operation.ID,
			)
		}
	default:
		return fmt.Errorf(
			"%w: operation %q has invalid rate-limit subject %q",
			ErrInvalid,
			operation.ID,
			policy.Subject,
		)
	}
	if policy.Limit == 0 || policy.Limit > uint64(math.MaxInt64) {
		return fmt.Errorf(
			"%w: operation %q has invalid rate-limit capacity %d",
			ErrInvalid,
			operation.ID,
			policy.Limit,
		)
	}
	if policy.Window < ratelimit.MinWindow ||
		policy.Window > ratelimit.MaxWindow {
		return fmt.Errorf(
			"%w: operation %q has invalid rate-limit window %s",
			ErrInvalid,
			operation.ID,
			policy.Window,
		)
	}
	return nil
}

func isWriteMethod(method HTTPMethod) bool {
	switch method {
	case MethodPost, MethodPut, MethodPatch, MethodDelete:
		return true
	default:
		return false
	}
}

func validateMigrationBundle(bundle MigrationBundle) error {
	if !validName(bundle.Name()) || len(bundle.sources) == 0 {
		return fmt.Errorf("%w: migration bundle %q", ErrInvalid, bundle.Name())
	}
	if !bundle.executor.empty() && !bundle.executor.complete() {
		return fmt.Errorf(
			"%w: migration bundle %q executor requires Up and EnsureCurrent",
			ErrInvalid,
			bundle.Name(),
		)
	}
	seen := make(map[Dialect]struct{}, len(bundle.sources))
	for _, source := range bundle.sources {
		if !validDialect(source.Dialect) || !validMigrationDirectory(source.Directory) {
			return fmt.Errorf("%w: migration bundle %q", ErrInvalid, bundle.Name())
		}
		if _, exists := seen[source.Dialect]; exists {
			return fmt.Errorf("%w: migration bundle %q repeats dialect %q", ErrDuplicate, bundle.Name(), source.Dialect)
		}
		seen[source.Dialect] = struct{}{}
	}
	return nil
}

func validName(value string) bool {
	return namePattern.MatchString(value)
}

func validPermissionCode(value string) bool {
	return permissionPattern.MatchString(value)
}

func validHTTPMethod(method HTTPMethod) bool {
	switch method {
	case MethodGet, MethodPost, MethodPut, MethodPatch, MethodDelete, MethodHead, MethodOptions:
		return true
	default:
		return false
	}
}

func validAPIPath(value string) bool {
	if !strings.HasPrefix(value, "/api/v1/") ||
		strings.ContainsAny(value, `?#\%:*`) ||
		strings.Contains(value, "//") {
		return false
	}
	if strings.HasSuffix(value, "/") || path.Clean(value) != value {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(value, "/"), "/")
	seenParameters := make(map[string]struct{})
	for index, segment := range segments {
		match := apiParameterPattern.FindStringSubmatch(segment)
		if match == nil {
			if !apiLiteralPattern.MatchString(segment) {
				return false
			}
			continue
		}
		name := match[1]
		if _, exists := seenParameters[name]; exists {
			return false
		}
		seenParameters[name] = struct{}{}
		if match[2] == "+" && index != len(segments)-1 {
			return false
		}
	}
	return true
}

func canonicalRouteShape(value string) string {
	segments := strings.Split(value, "/")
	for index, segment := range segments {
		match := apiParameterPattern.FindStringSubmatch(segment)
		if match == nil {
			continue
		}
		if match[2] == "+" {
			segments[index] = "{+}"
		} else {
			segments[index] = "{}"
		}
	}
	return strings.Join(segments, "/")
}

func jobKey(jobType string, version uint) string {
	return fmt.Sprintf("%s@%d", jobType, version)
}

func sameDTOType(first, second reflect.Type) bool {
	for first != nil && first.Kind() == reflect.Pointer {
		first = first.Elem()
	}
	for second != nil && second.Kind() == reflect.Pointer {
		second = second.Elem()
	}
	return first == second
}

func contractHasErrorStatus(contract APIContract, status int) bool {
	for _, candidate := range contract.ErrorStatuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func isNilModule(candidate Module) bool {
	if candidate == nil {
		return true
	}
	value := reflect.ValueOf(candidate)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
