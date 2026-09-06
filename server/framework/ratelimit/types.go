package ratelimit

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxNamespaceBytes bounds the stable policy namespace included in a bucket
	// identity.
	MaxNamespaceBytes = 64
	// MaxKeyBytes bounds the caller-supplied identity before it is hashed.
	MaxKeyBytes = 512
	// MaxCleanupBatch prevents a cleanup call from constructing an unbounded
	// delete statement.
	MaxCleanupBatch = 10_000
	// MinWindow avoids accidental near-zero policies and keeps persisted
	// timestamps practical across supported databases.
	MinWindow = time.Millisecond
	// MaxWindow supports monthly quotas without retaining accidental permanent
	// state.
	MaxWindow = 31 * 24 * time.Hour
)

var (
	ErrDatabaseRequired   = errors.New("rate limit database is required")
	ErrInvalidRequest     = errors.New("invalid rate limit request")
	ErrInvalidOption      = errors.New("invalid rate limit option")
	ErrInvalidState       = errors.New("invalid persisted rate limit state")
	ErrContention         = errors.New("rate limit contention retry budget exhausted")
	ErrUnsupportedDialect = errors.New("unsupported rate limit migration dialect")
)

// Request identifies one fixed-window bucket and the capacity to consume.
//
// Namespace should identify a stable policy such as "login.ip"; Key identifies
// the subject inside that policy. A bucket's namespace, key, and window form
// its persisted identity. Callers should keep Limit stable during a window;
// lowering it takes effect immediately without resetting prior usage.
type Request struct {
	Namespace string
	Key       string
	Limit     uint64
	Cost      uint64
	Window    time.Duration
}

// Decision describes the result of one atomic capacity-consumption attempt.
// RetryAfter is zero for allowed requests and precise for denied requests.
type Decision struct {
	Allowed    bool
	Limit      uint64
	Remaining  uint64
	RetryAfter time.Duration
	ResetAt    time.Time
}

// Limiter is the provider-neutral shared-state contract used by services.
// Implementations must consume capacity atomically across instances.
type Limiter interface {
	Consume(context.Context, Request) (Decision, error)
	CleanupExpired(context.Context, int) (int64, error)
}

// Clock allows deterministic window and cleanup behavior in tests.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time {
	return f()
}

type wallClock struct{}

func (wallClock) Now() time.Time {
	return time.Now()
}

func (request Request) validate() error {
	if !validNamespace(request.Namespace) {
		return fmt.Errorf("%w: namespace must be lowercase, bounded, and start with a letter", ErrInvalidRequest)
	}
	if !validKey(request.Key) {
		return fmt.Errorf("%w: key must be valid UTF-8 without control characters and at most %d bytes", ErrInvalidRequest, MaxKeyBytes)
	}
	if request.Limit == 0 || request.Limit > math.MaxInt64 {
		return fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidRequest, int64(math.MaxInt64))
	}
	if request.Cost == 0 || request.Cost > request.Limit {
		return fmt.Errorf("%w: cost must be between 1 and limit", ErrInvalidRequest)
	}
	if request.Window < MinWindow || request.Window > MaxWindow {
		return fmt.Errorf("%w: window must be between %s and %s", ErrInvalidRequest, MinWindow, MaxWindow)
	}
	return nil
}

func validNamespace(namespace string) bool {
	if len(namespace) == 0 || len(namespace) > MaxNamespaceBytes {
		return false
	}
	for index := range len(namespace) {
		character := namespace[index]
		if index == 0 {
			if character < 'a' || character > 'z' {
				return false
			}
			continue
		}
		switch {
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '.', character == ':', character == '_', character == '-':
		default:
			return false
		}
	}
	return true
}

func validKey(key string) bool {
	if len(key) == 0 || len(key) > MaxKeyBytes || !utf8.ValidString(key) || strings.TrimSpace(key) == "" {
		return false
	}
	for _, character := range key {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
