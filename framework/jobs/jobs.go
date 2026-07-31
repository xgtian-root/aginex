// Package jobs defines provider-neutral durable job contracts.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/xgtian-root/aginex/framework/authz"
	"gorm.io/gorm"
)

var (
	ErrInvalid             = errors.New("jobs: invalid request")
	ErrInvalidTransition   = errors.New("jobs: invalid state transition")
	ErrLeaseLost           = errors.New("jobs: lease lost")
	ErrIdempotencyConflict = errors.New("jobs: idempotency conflict")
	ErrNotFound            = errors.New("jobs: not found")
)

type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateDead      State = "dead"
)

type TraceContext struct {
	RequestID   string
	TraceParent string
}

type Job struct {
	ID             string
	Type           string
	Version        uint
	Payload        json.RawMessage
	PayloadSHA256  string
	IdempotencyKey string
	State          State
	ScheduledAt    time.Time
	Attempts       int
	MaxAttempts    int
	LockedBy       string
	LockedAt       *time.Time
	HeartbeatAt    *time.Time
	LastError      string
	CreatedBy      authz.Actor
	Trace          TraceContext
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

type EnqueueRequest struct {
	Type           string
	Version        uint
	Payload        json.RawMessage
	IdempotencyKey string
	ScheduledAt    time.Time
	MaxAttempts    int
	CreatedBy      authz.Actor
	Trace          TraceContext
}

type ClaimRequest struct {
	WorkerID string
	Limit    int
}

type EnqueueResult struct {
	Job     Job
	Created bool
}

type ListRequest struct {
	State    State
	Type     string
	Page     int
	PageSize int
}

type ListResult struct {
	Jobs     []Job
	Page     int
	PageSize int
	Total    int64
}

type Queue interface {
	Enqueue(context.Context, EnqueueRequest) (EnqueueResult, error)
	Claim(context.Context, ClaimRequest) ([]Job, error)
	Heartbeat(context.Context, string, string) error
	Succeed(context.Context, string, string) error
	Fail(context.Context, string, string, error) (State, error)
	RetryDead(context.Context, string) error
}

// TransactionalQueue can bind enqueue operations to an existing business
// transaction. Worker lifecycle methods remain available on the root queue.
type TransactionalQueue interface {
	Queue
	Bind(*gorm.DB) (Queue, error)
}

// Inspector provides read-only access to durable job metadata for protected
// operator APIs. Job payloads remain part of Job for worker use; HTTP adapters
// must project them into a safe response type.
type Inspector interface {
	List(context.Context, ListRequest) (ListResult, error)
}

// AdministrativeQueue is the complete durable queue contract used by API
// processes that expose protected operator controls.
type AdministrativeQueue interface {
	TransactionalQueue
	Inspector
}

var (
	typePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	workerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]*$`)
	jobIDPattern  = regexp.MustCompile(
		`^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[1-8][A-Fa-f0-9]{3}-[89ABab][A-Fa-f0-9]{3}-[A-Fa-f0-9]{12}$`,
	)
)

func (request *EnqueueRequest) Normalize(now time.Time) error {
	request.Type = strings.TrimSpace(request.Type)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.Trace.RequestID = strings.TrimSpace(request.Trace.RequestID)
	request.Trace.TraceParent = strings.TrimSpace(request.Trace.TraceParent)
	if !typePattern.MatchString(request.Type) || len(request.Type) > 120 {
		return fmt.Errorf("%w: invalid job type", ErrInvalid)
	}
	if request.Version == 0 {
		return fmt.Errorf("%w: job version must be positive", ErrInvalid)
	}
	if len(request.Payload) == 0 || !json.Valid(request.Payload) {
		return fmt.Errorf("%w: payload must be valid JSON", ErrInvalid)
	}
	if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 200 {
		return fmt.Errorf("%w: idempotency key must contain 1-200 characters", ErrInvalid)
	}
	if request.MaxAttempts == 0 {
		request.MaxAttempts = 10
	}
	if request.MaxAttempts < 1 || request.MaxAttempts > 100 {
		return fmt.Errorf("%w: max attempts must be between 1 and 100", ErrInvalid)
	}
	if request.ScheduledAt.IsZero() {
		request.ScheduledAt = now
	}
	request.ScheduledAt = request.ScheduledAt.UTC()
	if !validActor(request.CreatedBy) {
		return fmt.Errorf("%w: a user or system creator is required", ErrInvalid)
	}
	if len(request.Trace.RequestID) > 64 || len(request.Trace.TraceParent) > 512 {
		return fmt.Errorf("%w: trace metadata is too long", ErrInvalid)
	}
	return nil
}

func NormalizeClaim(request ClaimRequest) (ClaimRequest, error) {
	request.WorkerID = strings.TrimSpace(request.WorkerID)
	if !workerPattern.MatchString(request.WorkerID) || len(request.WorkerID) > 128 {
		return ClaimRequest{}, fmt.Errorf("%w: invalid worker ID", ErrInvalid)
	}
	if request.Limit == 0 {
		request.Limit = 1
	}
	if request.Limit < 1 || request.Limit > 100 {
		return ClaimRequest{}, fmt.Errorf("%w: claim limit must be between 1 and 100", ErrInvalid)
	}
	return request, nil
}

func NormalizeList(request ListRequest) (ListRequest, error) {
	request.State = State(strings.TrimSpace(string(request.State)))
	request.Type = strings.TrimSpace(request.Type)
	if request.State == "" {
		request.State = StateDead
	}
	if !validState(request.State) {
		return ListRequest{}, fmt.Errorf("%w: invalid job state", ErrInvalid)
	}
	if request.Type != "" &&
		(!typePattern.MatchString(request.Type) || len(request.Type) > 120) {
		return ListRequest{}, fmt.Errorf("%w: invalid job type", ErrInvalid)
	}
	if request.Page == 0 {
		request.Page = 1
	}
	if request.PageSize == 0 {
		request.PageSize = 20
	}
	if request.Page < 1 || request.Page > 1_000_000 {
		return ListRequest{}, fmt.Errorf(
			"%w: page must be between 1 and 1000000",
			ErrInvalid,
		)
	}
	if request.PageSize < 1 || request.PageSize > 100 {
		return ListRequest{}, fmt.Errorf(
			"%w: page size must be between 1 and 100",
			ErrInvalid,
		)
	}
	return request, nil
}

func NormalizeJobID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if !jobIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: invalid job ID", ErrInvalid)
	}
	return strings.ToLower(id), nil
}

// RetryDelay returns exponential backoff with symmetric jitter. jitter must be
// within [-1, 1], where 0 produces the exact exponential delay.
func RetryDelay(attempt int, base, maximum time.Duration, jitter float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if base <= 0 {
		base = time.Second
	}
	if maximum < base {
		maximum = 15 * time.Minute
	}
	exponent := attempt - 1
	if exponent > 30 {
		exponent = 30
	}
	delay := time.Duration(float64(base) * math.Pow(2, float64(exponent)))
	if delay > maximum || delay < 0 {
		delay = maximum
	}
	if jitter < -1 {
		jitter = -1
	}
	if jitter > 1 {
		jitter = 1
	}
	jittered := time.Duration(float64(delay) * (1 + 0.2*jitter))
	if jittered > maximum {
		return maximum
	}
	if jittered < 0 {
		return 0
	}
	return jittered
}

func validActor(actor authz.Actor) bool {
	if strings.TrimSpace(actor.ID) == "" {
		return false
	}
	return actor.Kind == authz.ActorKindUser || actor.Kind == authz.ActorKindSystem
}

func validState(state State) bool {
	switch state {
	case StatePending, StateRunning, StateSucceeded, StateFailed, StateDead:
		return true
	default:
		return false
	}
}
