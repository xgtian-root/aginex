package module

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

var (
	// ErrInvalid marks an invalid or incomplete module definition.
	ErrInvalid = errors.New("module: invalid definition")
	// ErrDuplicate marks a name, operation, route, or handler registered twice.
	ErrDuplicate = errors.New("module: duplicate registration")
)

// Module contributes validated metadata to an application registry.
type Module interface {
	Name() string
	Register(*Registry) error
}

// HTTPMethod is the canonical HTTP method attached to an operation.
type HTTPMethod string

const (
	MethodGet     HTTPMethod = "GET"
	MethodPost    HTTPMethod = "POST"
	MethodPut     HTTPMethod = "PUT"
	MethodPatch   HTTPMethod = "PATCH"
	MethodDelete  HTTPMethod = "DELETE"
	MethodHead    HTTPMethod = "HEAD"
	MethodOptions HTTPMethod = "OPTIONS"
)

// PermissionDefinition declares a lowercase resource:action permission.
type PermissionDefinition struct {
	Code        string
	Description string
}

// PolicyRef identifies the object-level policy a runtime authorizer must apply.
type PolicyRef string

// AuthenticationRef identifies the authenticator and OpenAPI security scheme
// used by one protected operation.
type AuthenticationRef string

// IdempotencyPolicy declares whether an authenticated write accepts an
// Idempotency-Key. The zero value disables framework idempotency middleware.
type IdempotencyPolicy string

const (
	// IdempotencyDisabled leaves the operation outside the idempotency
	// lifecycle.
	IdempotencyDisabled IdempotencyPolicy = ""
	// IdempotencyOptional accepts an optional Idempotency-Key. Requests without
	// the header execute normally; requests with it are claimed and replayed.
	IdempotencyOptional IdempotencyPolicy = "optional"
)

// Enabled reports whether the operation participates in the idempotency
// lifecycle.
func (policy IdempotencyPolicy) Enabled() bool {
	return policy != IdempotencyDisabled
}

// RateLimitSubject selects the stable request identity used for a shared
// rate-limit bucket.
type RateLimitSubject string

const (
	// RateLimitByIP keys the policy by the trusted client IP resolved by Gin.
	RateLimitByIP RateLimitSubject = "ip"
	// RateLimitByActor keys the policy by the authenticated framework actor.
	RateLimitByActor RateLimitSubject = "actor"
)

// RateLimitPolicy declares one fixed-window shared-state budget. The zero value
// disables framework rate-limit middleware. Namespace must remain stable across
// releases so all instances consume the same bucket.
type RateLimitPolicy struct {
	Namespace string
	Subject   RateLimitSubject
	Limit     uint64
	Window    time.Duration
}

// Enabled reports whether any rate-limit policy field was declared.
func (policy RateLimitPolicy) Enabled() bool {
	return policy != (RateLimitPolicy{})
}

// OperationDefinition describes a typed API operation without mounting a route.
//
// An operation is either explicitly Public or protected by both Permission and
// Policy plus an explicit Authentication scheme. Mixing the two modes, or
// omitting a protected field, is invalid.
type OperationDefinition struct {
	ID             string
	Method         HTTPMethod
	Path           string
	Public         bool
	Authentication AuthenticationRef
	Permission     string
	Policy         PolicyRef
	Idempotency    IdempotencyPolicy
	RateLimit      RateLimitPolicy
}

// Dialect identifies a database migration dialect.
type Dialect string

const (
	DialectSQLite     Dialect = "sqlite"
	DialectPostgreSQL Dialect = "postgres"
	DialectMySQL      Dialect = "mysql"
)

// MigrationSource points at one dialect-specific Goose migration directory.
type MigrationSource struct {
	Dialect   Dialect
	Directory string
}

// MigrationBundle is an immutable description of a module's migration sources.
//
// Its fields are intentionally private. NewMigrationBundle copies caller input,
// and Sources returns another copy.
type MigrationBundle struct {
	name     string
	sources  []MigrationSource
	executor MigrationExecutor
}

// NewMigrationBundle validates and copies dialect-specific migration sources.
func NewMigrationBundle(name string, sources ...MigrationSource) (MigrationBundle, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return MigrationBundle{}, fmt.Errorf("%w: migration bundle name %q", ErrInvalid, name)
	}
	if len(sources) == 0 {
		return MigrationBundle{}, fmt.Errorf("%w: migration bundle %q has no sources", ErrInvalid, name)
	}

	copied := append([]MigrationSource(nil), sources...)
	seen := make(map[Dialect]struct{}, len(copied))
	for i := range copied {
		source := &copied[i]
		source.Directory = strings.TrimSpace(source.Directory)
		if !validDialect(source.Dialect) {
			return MigrationBundle{}, fmt.Errorf("%w: migration bundle %q has unsupported dialect %q", ErrInvalid, name, source.Dialect)
		}
		if _, exists := seen[source.Dialect]; exists {
			return MigrationBundle{}, fmt.Errorf("%w: migration bundle %q repeats dialect %q", ErrDuplicate, name, source.Dialect)
		}
		seen[source.Dialect] = struct{}{}
		if !validMigrationDirectory(source.Directory) {
			return MigrationBundle{}, fmt.Errorf("%w: migration bundle %q has unsafe directory %q", ErrInvalid, name, source.Directory)
		}
	}
	sort.Slice(copied, func(i, j int) bool {
		return copied[i].Dialect < copied[j].Dialect
	})
	return MigrationBundle{name: name, sources: copied}, nil
}

// Name returns the stable bundle name.
func (b MigrationBundle) Name() string {
	return b.name
}

// Sources returns a copy of the bundle's migration source descriptions.
func (b MigrationBundle) Sources() []MigrationSource {
	return append([]MigrationSource(nil), b.sources...)
}

func (b MigrationBundle) clone() MigrationBundle {
	return MigrationBundle{
		name:     b.name,
		sources:  b.Sources(),
		executor: b.executor,
	}
}

// JobHandler handles a versioned JSON payload. The registry stores the
// function reference but never invokes it.
type JobHandler func(context.Context, json.RawMessage) error

// JobHandlerDefinition describes one version of a durable job handler.
type JobHandlerDefinition struct {
	Type    string
	Version uint
	Handle  JobHandler
}

// LifecycleHook contains optional start and stop callbacks. The registry stores
// callbacks but never invokes them.
type LifecycleHook struct {
	Name  string
	Start func(context.Context) error
	Stop  func(context.Context) error
}

func validDialect(dialect Dialect) bool {
	switch dialect {
	case DialectSQLite, DialectPostgreSQL, DialectMySQL:
		return true
	default:
		return false
	}
}

func validMigrationDirectory(directory string) bool {
	if directory == "" || strings.Contains(directory, `\`) || strings.HasPrefix(directory, "/") {
		return false
	}
	cleaned := path.Clean(directory)
	return cleaned == directory && cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}
