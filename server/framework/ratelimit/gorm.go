package ratelimit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// TableName is the opt-in shared-state table created by this package's
	// dialect-specific migrations.
	TableName = "aginex_rate_limit_windows"

	defaultMaxCASAttempts = 256
	maxCASAttempts        = 10_000
)

// GORMLimiter atomically consumes capacity with portable optimistic
// compare-and-swap updates. It contains no application-service dialect logic.
type GORMLimiter struct {
	db             *gorm.DB
	clock          Clock
	maxCASAttempts int
}

var _ Limiter = (*GORMLimiter)(nil)

type gormConfig struct {
	clock          Clock
	maxCASAttempts int
}

// GORMOption customizes a GORMLimiter.
type GORMOption func(*gormConfig) error

// WithClock injects the clock used to align windows and expire state.
func WithClock(clock Clock) GORMOption {
	return func(config *gormConfig) error {
		if nilInterface(clock) {
			return fmt.Errorf("%w: clock is required", ErrInvalidOption)
		}
		config.clock = clock
		return nil
	}
}

// WithMaxCASAttempts sets the bounded optimistic-concurrency retry budget.
func WithMaxCASAttempts(attempts int) GORMOption {
	return func(config *gormConfig) error {
		if attempts <= 0 || attempts > maxCASAttempts {
			return fmt.Errorf("%w: max CAS attempts must be between 1 and %d", ErrInvalidOption, maxCASAttempts)
		}
		config.maxCASAttempts = attempts
		return nil
	}
}

// NewGORM constructs a shared-state limiter. Its schema is never created
// implicitly; callers must run the package's opt-in Goose migrations.
func NewGORM(db *gorm.DB, options ...GORMOption) (*GORMLimiter, error) {
	if db == nil {
		return nil, ErrDatabaseRequired
	}
	config := gormConfig{
		clock:          wallClock{},
		maxCASAttempts: defaultMaxCASAttempts,
	}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: option %d is nil", ErrInvalidOption, index)
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}
	if nilInterface(config.clock) {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidOption)
	}
	return &GORMLimiter{
		db:             db,
		clock:          config.clock,
		maxCASAttempts: config.maxCASAttempts,
	}, nil
}

// Consume atomically consumes request.Cost units from a fixed window. Failed
// decisions do not consume additional capacity.
func (limiter *GORMLimiter) Consume(ctx context.Context, request Request) (Decision, error) {
	if ctx == nil {
		return Decision{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := request.validate(); err != nil {
		return Decision{}, err
	}
	if limiter == nil || limiter.db == nil || nilInterface(limiter.clock) {
		return Decision{}, ErrDatabaseRequired
	}

	now := wallTime(limiter.clock.Now())
	windowStartNS := alignedWindowStart(now.UnixNano(), request.Window.Nanoseconds())
	resetAt := time.Unix(0, windowStartNS+request.Window.Nanoseconds()).UTC()
	keyHash := bucketHash(request)
	limit := int64(request.Limit)
	cost := int64(request.Cost)

	for attempt := 0; attempt < limiter.maxCASAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return Decision{}, err
		}

		var current windowRecord
		err := limiter.db.WithContext(ctx).
			Where("key_hash = ?", keyHash).
			Take(&current).
			Error
		switch {
		case err == nil:
			if err := current.validate(request.Window); err != nil {
				return Decision{}, err
			}
			if current.WindowStartedAtNS == windowStartNS {
				if current.RequestCount > limit || cost > limit-current.RequestCount {
					return deniedDecision(request, current.RequestCount, now, resetAt), nil
				}
				nextCount := current.RequestCount + cost
				updated, updateErr := limiter.compareAndSwap(ctx, current, map[string]any{
					"request_count": nextCount,
					"revision":      current.Revision + 1,
				})
				if updateErr != nil {
					return Decision{}, updateErr
				}
				if updated {
					return allowedDecision(request, nextCount, resetAt), nil
				}
			} else {
				updated, updateErr := limiter.compareAndSwap(ctx, current, map[string]any{
					"window_started_at_ns": windowStartNS,
					"request_count":        cost,
					"expires_at_ns":        resetAt.UnixNano(),
					"revision":             current.Revision + 1,
				})
				if updateErr != nil {
					return Decision{}, updateErr
				}
				if updated {
					return allowedDecision(request, cost, resetAt), nil
				}
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			insertToken, tokenErr := newInsertToken()
			if tokenErr != nil {
				return Decision{}, tokenErr
			}
			row := windowRecord{
				KeyHash:           keyHash,
				WindowStartedAtNS: windowStartNS,
				RequestCount:      cost,
				ExpiresAtNS:       resetAt.UnixNano(),
				Revision:          1,
				InsertToken:       insertToken,
			}
			result := limiter.db.WithContext(ctx).
				Clauses(clause.OnConflict{DoNothing: true}).
				Create(&row)
			if result.Error != nil {
				return Decision{}, result.Error
			}
			// RowsAffected cannot distinguish an insert from MySQL's duplicate
			// no-op when clientFoundRows is enabled. The immutable random token
			// proves which contender created the row on every dialect.
			var persisted windowRecord
			queryErr := limiter.db.WithContext(ctx).
				Select("key_hash", "insert_token").
				Where("key_hash = ?", keyHash).
				Take(&persisted).
				Error
			if queryErr != nil && !errors.Is(queryErr, gorm.ErrRecordNotFound) {
				return Decision{}, queryErr
			}
			if queryErr == nil && persisted.InsertToken == insertToken {
				return allowedDecision(request, cost, resetAt), nil
			}
		default:
			return Decision{}, err
		}
		runtime.Gosched()
	}
	return Decision{}, ErrContention
}

// CleanupExpired removes at most batchSize windows whose reset time is not
// later than the injected clock. A second expiry predicate on the delete keeps
// a concurrently reset row alive.
func (limiter *GORMLimiter) CleanupExpired(ctx context.Context, batchSize int) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if batchSize <= 0 || batchSize > MaxCleanupBatch {
		return 0, fmt.Errorf("%w: cleanup batch must be between 1 and %d", ErrInvalidRequest, MaxCleanupBatch)
	}
	if limiter == nil || limiter.db == nil || nilInterface(limiter.clock) {
		return 0, ErrDatabaseRequired
	}

	cutoffNS := wallTime(limiter.clock.Now()).UnixNano()
	var keys []string
	err := limiter.db.WithContext(ctx).
		Model(&windowRecord{}).
		Where("expires_at_ns <= ?", cutoffNS).
		Order("expires_at_ns ASC").
		Limit(batchSize).
		Pluck("key_hash", &keys).
		Error
	if err != nil || len(keys) == 0 {
		return 0, err
	}
	result := limiter.db.WithContext(ctx).
		Where("expires_at_ns <= ? AND key_hash IN ?", cutoffNS, keys).
		Delete(&windowRecord{})
	return result.RowsAffected, result.Error
}

func (limiter *GORMLimiter) compareAndSwap(
	ctx context.Context,
	current windowRecord,
	updates map[string]any,
) (bool, error) {
	result := limiter.db.WithContext(ctx).
		Model(&windowRecord{}).
		Where("key_hash = ? AND revision = ?", current.KeyHash, current.Revision).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

type windowRecord struct {
	KeyHash           string `gorm:"column:key_hash;primaryKey;size:64"`
	WindowStartedAtNS int64  `gorm:"column:window_started_at_ns;not null"`
	RequestCount      int64  `gorm:"column:request_count;not null"`
	ExpiresAtNS       int64  `gorm:"column:expires_at_ns;not null;index"`
	Revision          int64  `gorm:"column:revision;not null"`
	InsertToken       string `gorm:"column:insert_token;size:32;not null"`
}

func (windowRecord) TableName() string {
	return TableName
}

func (record windowRecord) validate(window time.Duration) error {
	windowNS := window.Nanoseconds()
	if record.KeyHash == "" ||
		record.RequestCount < 0 ||
		record.Revision <= 0 ||
		len(record.InsertToken) != 32 ||
		record.WindowStartedAtNS > record.ExpiresAtNS ||
		record.ExpiresAtNS-record.WindowStartedAtNS != windowNS {
		return fmt.Errorf("%w: corrupt window %q", ErrInvalidState, record.KeyHash)
	}
	return nil
}

func allowedDecision(request Request, used int64, resetAt time.Time) Decision {
	return Decision{
		Allowed:   true,
		Limit:     request.Limit,
		Remaining: request.Limit - uint64(used),
		ResetAt:   resetAt,
	}
}

func deniedDecision(request Request, used int64, now, resetAt time.Time) Decision {
	remaining := uint64(0)
	if used >= 0 && uint64(used) < request.Limit {
		remaining = request.Limit - uint64(used)
	}
	retryAfter := resetAt.Sub(now)
	if retryAfter < 0 {
		retryAfter = 0
	}
	return Decision{
		Allowed:    false,
		Limit:      request.Limit,
		Remaining:  remaining,
		RetryAfter: retryAfter,
		ResetAt:    resetAt,
	}
}

func bucketHash(request Request) string {
	hasher := sha256.New()
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(request.Namespace)))
	_, _ = hasher.Write(size[:])
	_, _ = hasher.Write([]byte(request.Namespace))
	binary.BigEndian.PutUint64(size[:], uint64(len(request.Key)))
	_, _ = hasher.Write(size[:])
	_, _ = hasher.Write([]byte(request.Key))
	binary.BigEndian.PutUint64(size[:], uint64(request.Window.Nanoseconds()))
	_, _ = hasher.Write(size[:])
	return hex.EncodeToString(hasher.Sum(nil))
}

func alignedWindowStart(nowNS, windowNS int64) int64 {
	remainder := nowNS % windowNS
	if remainder < 0 {
		remainder += windowNS
	}
	return nowNS - remainder
}

func wallTime(value time.Time) time.Time {
	return time.Unix(0, value.UnixNano()).UTC()
}

func newInsertToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate rate limit insert token: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
