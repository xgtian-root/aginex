package module

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xgtian-root/aginex/server/framework/authz"
)

// ParameterLocation identifies where an OpenAPI operation parameter is read.
// Request bodies are represented by APIContract.RequestDTO instead.
type ParameterLocation string

const (
	ParameterPath   ParameterLocation = "path"
	ParameterQuery  ParameterLocation = "query"
	ParameterHeader ParameterLocation = "header"
)

// ParameterType is the JSON-schema primitive used for an operation parameter.
type ParameterType string

const (
	ParameterString  ParameterType = "string"
	ParameterInteger ParameterType = "integer"
	ParameterBoolean ParameterType = "boolean"
)

// ParameterDefinition is a provider-neutral OpenAPI parameter description.
// Enum values are copied by the registry. Default must be nil or a primitive
// matching Type.
type ParameterDefinition struct {
	Name        string
	In          ParameterLocation
	Description string
	Required    bool
	Type        ParameterType
	Format      string
	Pattern     string
	Default     any
	Enum        []string
	Minimum     *float64
	Maximum     *float64
	MaxLength   *int
}

// APIContract is the typed, transport-facing contract for one registered
// operation. Authentication security is derived from OperationDefinition:
// modules cannot accidentally document a protected operation as public.
type APIContract struct {
	OperationID       string
	Summary           string
	Tag               string
	SuccessStatus     int
	RequestDTO        reflect.Type
	RequestMediaType  string
	ResponseDTO       reflect.Type
	ResponseMediaType string
	Parameters        []ParameterDefinition
	ErrorStatuses     []int
}

// AuthorizedHandler receives the admitted actor, grant scope, and registered
// policy for a protected operation.
type AuthorizedHandler func(*gin.Context, RequestAuthorization)

// IdempotencyReplay is the bounded, immutable input available when a completed
// response is about to be replayed. Scoped operations use its route parameters
// or sanitized stored body to resolve the current resource without receiving a
// response writer or an escape hatch around the authorization usage guard.
type IdempotencyReplay struct {
	pathParameters map[string]string
	responseStatus int
	responseBody   []byte
}

// NewIdempotencyReplay copies replay metadata before passing it to a compiled
// module's ReplayAuthorizer.
func NewIdempotencyReplay(
	pathParameters map[string]string,
	responseStatus int,
	responseBody []byte,
) IdempotencyReplay {
	parameters := make(map[string]string, len(pathParameters))
	for name, value := range pathParameters {
		parameters[name] = value
	}
	return IdempotencyReplay{
		pathParameters: parameters,
		responseStatus: responseStatus,
		responseBody:   append([]byte(nil), responseBody...),
	}
}

// PathParameter returns one canonical Gin route parameter.
func (replay IdempotencyReplay) PathParameter(name string) string {
	return replay.pathParameters[name]
}

// ResponseStatus returns the stored successful HTTP status.
func (replay IdempotencyReplay) ResponseStatus() int {
	return replay.responseStatus
}

// ResponseBody returns a defensive copy of the sanitized stored response.
func (replay IdempotencyReplay) ResponseBody() []byte {
	return append([]byte(nil), replay.responseBody...)
}

// ReplayAuthorizer must resolve the current object/query authorization before
// a cached response is written. Returning nil is insufficient by itself: the
// runtime also verifies that the supplied RequestAuthorization recorded a
// successful Check or Scope execution for the route's declared mode.
type ReplayAuthorizer func(
	context.Context,
	RequestAuthorization,
	IdempotencyReplay,
) error

// AuthorizationMode declares how a protected handler consumes its admitted
// decision. It is enforced before a successful response can be committed.
type AuthorizationMode string

const (
	// AuthorizationAll is for globally scoped operations. It only succeeds
	// when policy admission resolved an all grant.
	AuthorizationAll AuthorizationMode = "all"
	// AuthorizationActor is for self-service operations whose target is
	// derived exclusively from the authenticated actor, never client input.
	AuthorizationActor AuthorizationMode = "actor"
	// AuthorizationObject requires a successful RequestAuthorization.Check.
	AuthorizationObject AuthorizationMode = "object"
	// AuthorizationQuery requires a successful RequestAuthorization.Scope.
	AuthorizationQuery AuthorizationMode = "query"
)

// HTTPRoute binds one registered operation to exactly one public or protected
// Gin handler. Route middleware runs after Aginex's authentication and policy
// admission middleware and before the final handler. Security-sensitive global
// middleware remains application-core configuration rather than
// module-controlled state.
type HTTPRoute struct {
	OperationID       string
	Authorization     AuthorizationMode
	ReplayAuthorizer  ReplayAuthorizer
	Middleware        []gin.HandlerFunc
	Handler           gin.HandlerFunc
	AuthorizedHandler AuthorizedHandler
}

// AuthorizationPolicy associates an operation PolicyRef with an executable
// object/query policy. Implementations must be immutable after registration.
type AuthorizationPolicy struct {
	Ref    PolicyRef
	Policy authz.Policy
}

// ReadinessRequirement controls whether a failed dependency removes the
// process from service or is reported only to observability.
type ReadinessRequirement string

const (
	ReadinessRequired ReadinessRequirement = "required"
	ReadinessOptional ReadinessRequirement = "optional"
)

// ReadinessCheck is a bounded, provider-neutral dependency probe registered by
// a compiled-in module. Check must not mutate schema or application data.
type ReadinessCheck struct {
	Name        string
	Requirement ReadinessRequirement
	Timeout     time.Duration
	Check       func(context.Context) error
}

// AuthenticationKind identifies the supported transport credential shape.
type AuthenticationKind string

const (
	AuthenticationCookie AuthenticationKind = "cookie"
	AuthenticationBearer AuthenticationKind = "bearer"
)

// AuthenticationScheme binds a protected operation's credential middleware to
// its OpenAPI security description. A successful middleware must attach an
// actor with ContextWithAuthenticatedActor before calling the next handler.
type AuthenticationScheme struct {
	Ref          AuthenticationRef
	Kind         AuthenticationKind
	CookieName   string
	BearerFormat string
	Description  string
	Middleware   gin.HandlerFunc
}

var (
	parameterNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	formatPattern        = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
)

func validateHTTPRoute(route HTTPRoute) error {
	route.OperationID = strings.TrimSpace(route.OperationID)
	if !operationIDPattern.MatchString(route.OperationID) ||
		(route.Handler == nil) == (route.AuthorizedHandler == nil) {
		return fmt.Errorf("%w: invalid HTTP route for operation %q", ErrInvalid, route.OperationID)
	}
	switch route.Authorization {
	case "", AuthorizationAll, AuthorizationActor,
		AuthorizationObject, AuthorizationQuery:
	default:
		return fmt.Errorf(
			"%w: operation %q has invalid authorization mode %q",
			ErrInvalid,
			route.OperationID,
			route.Authorization,
		)
	}
	for _, middleware := range route.Middleware {
		if middleware == nil {
			return fmt.Errorf("%w: operation %q has nil middleware", ErrInvalid, route.OperationID)
		}
	}
	return nil
}

func cloneHTTPRoute(route HTTPRoute) HTTPRoute {
	route.Middleware = append([]gin.HandlerFunc(nil), route.Middleware...)
	return route
}

func validateReadinessCheck(check ReadinessCheck) error {
	check.Name = strings.TrimSpace(check.Name)
	if !validName(check.Name) ||
		(check.Requirement != ReadinessRequired &&
			check.Requirement != ReadinessOptional) ||
		check.Timeout <= 0 ||
		check.Timeout > time.Minute ||
		check.Check == nil {
		return fmt.Errorf(
			"%w: invalid readiness check %q",
			ErrInvalid,
			check.Name,
		)
	}
	return nil
}

func validateAPIContract(contract APIContract) error {
	contract.OperationID = strings.TrimSpace(contract.OperationID)
	contract.Summary = strings.TrimSpace(contract.Summary)
	contract.Tag = strings.TrimSpace(contract.Tag)
	contract.RequestMediaType = strings.TrimSpace(contract.RequestMediaType)
	contract.ResponseMediaType = strings.TrimSpace(contract.ResponseMediaType)
	if !operationIDPattern.MatchString(contract.OperationID) ||
		contract.Summary == "" ||
		contract.Tag == "" ||
		contract.SuccessStatus < http.StatusOK ||
		contract.SuccessStatus >= http.StatusMultipleChoices {
		return fmt.Errorf("%w: invalid API contract for operation %q", ErrInvalid, contract.OperationID)
	}
	if contract.SuccessStatus == http.StatusNoContent && contract.ResponseDTO != nil {
		return fmt.Errorf("%w: no-content operation %q declares a response DTO", ErrInvalid, contract.OperationID)
	}
	if err := validateDTOType(contract.RequestDTO); err != nil {
		return fmt.Errorf("%w: operation %q request DTO: %v", ErrInvalid, contract.OperationID, err)
	}
	if err := validateDTOType(contract.ResponseDTO); err != nil {
		return fmt.Errorf("%w: operation %q response DTO: %v", ErrInvalid, contract.OperationID, err)
	}
	if contract.RequestDTO == nil && contract.RequestMediaType != "" {
		return fmt.Errorf(
			"%w: operation %q declares request media without a request DTO",
			ErrInvalid,
			contract.OperationID,
		)
	}
	if contract.ResponseDTO == nil && contract.ResponseMediaType != "" {
		return fmt.Errorf(
			"%w: operation %q declares response media without a response DTO",
			ErrInvalid,
			contract.OperationID,
		)
	}
	if err := validateMediaType(contract.RequestMediaType); err != nil {
		return fmt.Errorf("%w: operation %q request media type: %v", ErrInvalid, contract.OperationID, err)
	}
	if err := validateMediaType(contract.ResponseMediaType); err != nil {
		return fmt.Errorf("%w: operation %q response media type: %v", ErrInvalid, contract.OperationID, err)
	}

	seenParameters := make(map[string]struct{}, len(contract.Parameters))
	for _, parameter := range contract.Parameters {
		if err := validateParameter(parameter); err != nil {
			return fmt.Errorf("%w: operation %q parameter: %v", ErrInvalid, contract.OperationID, err)
		}
		key := string(parameter.In) + "\x00" + strings.ToLower(parameter.Name)
		if _, exists := seenParameters[key]; exists {
			return fmt.Errorf("%w: operation %q repeats parameter %s", ErrDuplicate, contract.OperationID, parameter.Name)
		}
		seenParameters[key] = struct{}{}
	}

	seenStatuses := make(map[int]struct{}, len(contract.ErrorStatuses))
	for _, status := range contract.ErrorStatuses {
		if status < http.StatusBadRequest || status > 599 || status == contract.SuccessStatus {
			return fmt.Errorf("%w: operation %q has invalid error status %d", ErrInvalid, contract.OperationID, status)
		}
		if _, exists := seenStatuses[status]; exists {
			return fmt.Errorf("%w: operation %q repeats error status %d", ErrDuplicate, contract.OperationID, status)
		}
		seenStatuses[status] = struct{}{}
	}
	return nil
}

func validateDTOType(value reflect.Type) error {
	if value == nil {
		return nil
	}
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct, reflect.Slice, reflect.Array:
		return nil
	default:
		return fmt.Errorf("unsupported type %s", value)
	}
}

func validateMediaType(value string) error {
	if value == "" {
		return nil
	}
	parsed, parameters, err := mime.ParseMediaType(value)
	if err != nil || parsed != value || len(parameters) != 0 || !strings.Contains(parsed, "/") {
		return fmt.Errorf("invalid media type %q", value)
	}
	return nil
}

func validateParameter(parameter ParameterDefinition) error {
	parameter.Name = strings.TrimSpace(parameter.Name)
	parameter.Description = strings.TrimSpace(parameter.Description)
	parameter.Format = strings.TrimSpace(parameter.Format)
	parameter.Pattern = strings.TrimSpace(parameter.Pattern)
	if !parameterNamePattern.MatchString(parameter.Name) || parameter.Description == "" {
		return fmt.Errorf("name and description are required")
	}
	switch parameter.In {
	case ParameterPath, ParameterQuery, ParameterHeader:
	default:
		return fmt.Errorf("unsupported location %q", parameter.In)
	}
	if parameter.In == ParameterPath && !parameter.Required {
		return fmt.Errorf("path parameter %q must be required", parameter.Name)
	}
	switch parameter.Type {
	case ParameterString:
		if parameter.Minimum != nil || parameter.Maximum != nil {
			return fmt.Errorf("string parameter %q has numeric bounds", parameter.Name)
		}
		if parameter.Default != nil {
			if _, ok := parameter.Default.(string); !ok {
				return fmt.Errorf("string parameter %q has a non-string default", parameter.Name)
			}
		}
	case ParameterInteger:
		if parameter.MaxLength != nil || len(parameter.Enum) != 0 || parameter.Pattern != "" {
			return fmt.Errorf("integer parameter %q has string constraints", parameter.Name)
		}
		switch parameter.Default.(type) {
		case nil, int, int32, int64:
		default:
			return fmt.Errorf("integer parameter %q has a non-integer default", parameter.Name)
		}
	case ParameterBoolean:
		if parameter.Minimum != nil || parameter.Maximum != nil || parameter.MaxLength != nil || len(parameter.Enum) != 0 || parameter.Pattern != "" {
			return fmt.Errorf("boolean parameter %q has incompatible constraints", parameter.Name)
		}
		if parameter.Default != nil {
			if _, ok := parameter.Default.(bool); !ok {
				return fmt.Errorf("boolean parameter %q has a non-boolean default", parameter.Name)
			}
		}
	default:
		return fmt.Errorf("unsupported type %q", parameter.Type)
	}
	if parameter.Format != "" && !formatPattern.MatchString(parameter.Format) {
		return fmt.Errorf("parameter %q has invalid format", parameter.Name)
	}
	if parameter.Pattern != "" {
		if _, err := regexp.Compile(parameter.Pattern); err != nil {
			return fmt.Errorf("parameter %q has invalid pattern", parameter.Name)
		}
	}
	if parameter.Minimum != nil && parameter.Maximum != nil && *parameter.Minimum > *parameter.Maximum {
		return fmt.Errorf("parameter %q has reversed numeric bounds", parameter.Name)
	}
	if parameter.MaxLength != nil && *parameter.MaxLength < 1 {
		return fmt.Errorf("parameter %q has invalid maximum length", parameter.Name)
	}
	for _, value := range parameter.Enum {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("parameter %q has invalid enum value", parameter.Name)
		}
	}
	return nil
}

func cloneAPIContract(contract APIContract) APIContract {
	contract.Parameters = append([]ParameterDefinition(nil), contract.Parameters...)
	for index := range contract.Parameters {
		parameter := &contract.Parameters[index]
		parameter.Enum = append([]string(nil), parameter.Enum...)
		if parameter.Minimum != nil {
			value := *parameter.Minimum
			parameter.Minimum = &value
		}
		if parameter.Maximum != nil {
			value := *parameter.Maximum
			parameter.Maximum = &value
		}
		if parameter.MaxLength != nil {
			value := *parameter.MaxLength
			parameter.MaxLength = &value
		}
	}
	contract.ErrorStatuses = append([]int(nil), contract.ErrorStatuses...)
	return contract
}

func sortAPIContracts(contracts []APIContract) {
	sort.Slice(contracts, func(i, j int) bool {
		return contracts[i].OperationID < contracts[j].OperationID
	})
}

func validateAuthorizationPolicy(definition AuthorizationPolicy) error {
	definition.Ref = PolicyRef(strings.TrimSpace(string(definition.Ref)))
	if !validName(string(definition.Ref)) || policyIsNil(definition.Policy) {
		return fmt.Errorf("%w: invalid authorization policy %q", ErrInvalid, definition.Ref)
	}
	return nil
}

func validateAuthenticationScheme(definition AuthenticationScheme) error {
	definition.Ref = AuthenticationRef(
		strings.TrimSpace(string(definition.Ref)),
	)
	definition.CookieName = strings.TrimSpace(definition.CookieName)
	definition.BearerFormat = strings.TrimSpace(definition.BearerFormat)
	definition.Description = strings.TrimSpace(definition.Description)
	if !operationIDPattern.MatchString(string(definition.Ref)) ||
		definition.Description == "" ||
		definition.Middleware == nil {
		return fmt.Errorf(
			"%w: invalid authentication scheme %q",
			ErrInvalid,
			definition.Ref,
		)
	}
	switch definition.Kind {
	case AuthenticationCookie:
		if definition.CookieName == "" || definition.BearerFormat != "" {
			return fmt.Errorf(
				"%w: invalid cookie authentication scheme %q",
				ErrInvalid,
				definition.Ref,
			)
		}
	case AuthenticationBearer:
		if definition.CookieName != "" {
			return fmt.Errorf(
				"%w: invalid bearer authentication scheme %q",
				ErrInvalid,
				definition.Ref,
			)
		}
	default:
		return fmt.Errorf(
			"%w: unsupported authentication kind %q",
			ErrInvalid,
			definition.Kind,
		)
	}
	return nil
}

func operationHasPathParameter(path, name string) bool {
	return strings.Contains(path, "{"+name+"}") || strings.Contains(path, "{"+name+"+}")
}

func policyIsNil(policy authz.Policy) bool {
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
