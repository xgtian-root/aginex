package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxActorIDBytes bounds the authenticated subject persisted with a claim.
	MaxActorIDBytes = 160
	// MaxMethodBytes bounds an HTTP method token.
	MaxMethodBytes = 16
	// MaxRouteBytes bounds a registered route template.
	MaxRouteBytes = 512
	// MaxKeyBytes bounds the caller-supplied Idempotency-Key before hashing.
	MaxKeyBytes = 200
	// MaxResponseBodyBytes bounds a cached HTTP response.
	MaxResponseBodyBytes = 1 << 20
	// MaxResponseHeadersBytes bounds the canonical persisted header document.
	MaxResponseHeadersBytes = 8 << 10
	// MaxCleanupBatch prevents unbounded cleanup statements.
	MaxCleanupBatch = 10_000
	// MinLeaseDuration prevents accidental immediately-expired execution
	// leases.
	MinLeaseDuration = time.Second
	// MaxLeaseDuration bounds how long a crashed caller can block retries.
	MaxLeaseDuration = 15 * time.Minute
	// MinTTL avoids records expiring before a practical replay can occur.
	MinTTL = time.Minute
	// MaxTTL bounds retained idempotency state.
	MaxTTL = 31 * 24 * time.Hour
)

var (
	ErrDatabaseRequired   = errors.New("idempotency database is required")
	ErrInvalidRequest     = errors.New("invalid idempotency request")
	ErrInvalidOption      = errors.New("invalid idempotency option")
	ErrInvalidState       = errors.New("invalid persisted idempotency state")
	ErrConflict           = errors.New("idempotency key belongs to a different request")
	ErrLeaseLost          = errors.New("idempotency execution lease lost")
	ErrResponseTooLarge   = errors.New("idempotency response exceeds storage bounds")
	ErrUnsafeResponse     = errors.New("idempotency response contains unsafe data")
	ErrContention         = errors.New("idempotency contention retry budget exhausted")
	ErrUnsupportedDialect = errors.New("unsupported idempotency migration dialect")
)

// ActorKind identifies the principal owning an idempotency key.
type ActorKind string

const (
	ActorKindUser    ActorKind = "user"
	ActorKindSystem  ActorKind = "system"
	ActorKindService ActorKind = "service"
)

// Actor is the bounded authenticated identity used to isolate claims. Grants,
// roles, email addresses, and other identity data must not be copied here.
type Actor struct {
	Kind ActorKind
	ID   string
}

// ClaimRequest describes one normalized HTTP request. RequestDigest must be a
// lowercase SHA-256 hex digest over every request component whose change would
// alter the operation's result (normally canonical query, headers, and body).
type ClaimRequest struct {
	Actor         Actor
	Method        string
	Route         string
	Key           string
	RequestDigest string
}

// Disposition tells a caller whether it may execute, should retry after the
// active lease, or can replay a completed response.
type Disposition string

const (
	DispositionExecute    Disposition = "execute"
	DispositionInProgress Disposition = "in_progress"
	DispositionReplay     Disposition = "replay"
)

// Lease is an opaque execution capability. Token must never be logged or sent
// to the client.
type Lease struct {
	ID        string
	Token     string
	ExpiresAt time.Time
}

// Response is the bounded HTTP response representation safe to persist and
// replay. Non-empty bodies must be JSON so sensitive field names can be
// rejected. Only an explicit set of non-secret response headers is accepted.
type Response struct {
	Status      int
	ContentType string
	Headers     http.Header
	Body        []byte
}

// ClaimResult is the outcome of Claim. Lease is populated only for execute;
// Response only for replay.
type ClaimResult struct {
	Disposition     Disposition
	Lease           Lease
	Response        *Response
	RetryAfter      time.Duration
	LeaseExpiresAt  time.Time
	RecordExpiresAt time.Time
}

// CompletionResult reports whether a response was retained. A server error is
// released, not cached, unless WithCacheServerErrors explicitly enables it.
type CompletionResult struct {
	Stored bool
}

// Store is the provider-neutral idempotency lifecycle.
type Store interface {
	Claim(context.Context, ClaimRequest) (ClaimResult, error)
	Renew(context.Context, Lease) (Lease, error)
	Complete(context.Context, Lease, Response) (CompletionResult, error)
	Abandon(context.Context, Lease) error
	CleanupExpired(context.Context, int) (int64, error)
}

// Clock supports deterministic lease and expiry behavior.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

func (function ClockFunc) Now() time.Time {
	return function()
}

type wallClock struct{}

func (wallClock) Now() time.Time {
	return time.Now()
}

var methodPattern = regexp.MustCompile(`^[A-Z][A-Z0-9!#$%&'*+.^_` + "`" + `|~-]*$`)

func (request ClaimRequest) normalize() (ClaimRequest, error) {
	request.Method = strings.ToUpper(strings.TrimSpace(request.Method))
	if err := request.Actor.validate(); err != nil {
		return ClaimRequest{}, err
	}
	if len(request.Method) == 0 ||
		len(request.Method) > MaxMethodBytes ||
		!methodPattern.MatchString(request.Method) {
		return ClaimRequest{}, fmt.Errorf("%w: invalid HTTP method", ErrInvalidRequest)
	}
	if !validRoute(request.Route) {
		return ClaimRequest{}, fmt.Errorf(
			"%w: route must be an absolute, bounded template without query, fragment, or control characters",
			ErrInvalidRequest,
		)
	}
	if !validKey(request.Key) {
		return ClaimRequest{}, fmt.Errorf(
			"%w: key must contain 1-%d visible ASCII characters without spaces",
			ErrInvalidRequest,
			MaxKeyBytes,
		)
	}
	if !validHex(request.RequestDigest, sha256.Size*2) {
		return ClaimRequest{}, fmt.Errorf(
			"%w: request digest must be a lowercase SHA-256 hex value",
			ErrInvalidRequest,
		)
	}
	return request, nil
}

func (actor Actor) validate() error {
	switch actor.Kind {
	case ActorKindUser, ActorKindSystem, ActorKindService:
	default:
		return fmt.Errorf("%w: invalid actor kind", ErrInvalidRequest)
	}
	if len(actor.ID) == 0 ||
		len(actor.ID) > MaxActorIDBytes ||
		!utf8.ValidString(actor.ID) ||
		strings.TrimSpace(actor.ID) != actor.ID {
		return fmt.Errorf("%w: invalid actor ID", ErrInvalidRequest)
	}
	for _, character := range actor.ID {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: invalid actor ID", ErrInvalidRequest)
		}
	}
	return nil
}

func validRoute(route string) bool {
	if len(route) == 0 ||
		len(route) > MaxRouteBytes ||
		!utf8.ValidString(route) ||
		route[0] != '/' ||
		strings.ContainsAny(route, "?#\\") ||
		strings.Contains(route, "//") ||
		strings.TrimSpace(route) != route {
		return false
	}
	for _, character := range route {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func validKey(key string) bool {
	if len(key) == 0 || len(key) > MaxKeyBytes {
		return false
	}
	for index := range len(key) {
		if key[index] < 0x21 || key[index] > 0x7e {
			return false
		}
	}
	return true
}

func validHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if !((character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func (lease Lease) validate() error {
	if !validHex(lease.ID, sha256.Size*2) || !validHex(lease.Token, 32) {
		return fmt.Errorf("%w: invalid execution lease", ErrInvalidRequest)
	}
	return nil
}

// Digest returns the lowercase SHA-256 digest of canonical request bytes.
func Digest(canonicalRequest []byte) string {
	digest := sha256.Sum256(canonicalRequest)
	return hex.EncodeToString(digest[:])
}
