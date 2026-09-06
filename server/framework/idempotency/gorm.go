package idempotency

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"runtime"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// TableName is the opt-in state table created by this package's migrations.
	TableName = "aginex_idempotency_records"

	defaultLeaseDuration  = 30 * time.Second
	defaultTTL            = 24 * time.Hour
	defaultMaxCASAttempts = 256
	maxCASAttempts        = 10_000
)

type recordState string

const (
	stateInProgress recordState = "in_progress"
	stateCompleted  recordState = "completed"
)

// GORMStore implements Store with portable optimistic compare-and-swap
// operations. Services never branch on the active database dialect.
type GORMStore struct {
	db                *gorm.DB
	clock             Clock
	leaseDuration     time.Duration
	ttl               time.Duration
	cacheServerErrors bool
	maxCASAttempts    int
}

var _ Store = (*GORMStore)(nil)

type gormConfig struct {
	clock             Clock
	leaseDuration     time.Duration
	ttl               time.Duration
	cacheServerErrors bool
	maxCASAttempts    int
}

// GORMOption customizes a GORMStore.
type GORMOption func(*gormConfig) error

// WithClock injects the clock used for lease and retention decisions.
func WithClock(clock Clock) GORMOption {
	return func(config *gormConfig) error {
		if nilInterface(clock) {
			return fmt.Errorf("%w: clock is required", ErrInvalidOption)
		}
		config.clock = clock
		return nil
	}
}

// WithLeaseDuration configures how long one execution claim remains exclusive
// without renewal.
func WithLeaseDuration(duration time.Duration) GORMOption {
	return func(config *gormConfig) error {
		if duration < MinLeaseDuration || duration > MaxLeaseDuration {
			return fmt.Errorf(
				"%w: lease duration must be between %s and %s",
				ErrInvalidOption,
				MinLeaseDuration,
				MaxLeaseDuration,
			)
		}
		config.leaseDuration = duration
		return nil
	}
}

// WithTTL configures retention after a claim or completion.
func WithTTL(ttl time.Duration) GORMOption {
	return func(config *gormConfig) error {
		if ttl < MinTTL || ttl > MaxTTL {
			return fmt.Errorf(
				"%w: TTL must be between %s and %s",
				ErrInvalidOption,
				MinTTL,
				MaxTTL,
			)
		}
		config.ttl = ttl
		return nil
	}
}

// WithCacheServerErrors opts into replaying 5xx responses. The safer default
// releases a failed claim so a later caller can retry.
func WithCacheServerErrors(enabled bool) GORMOption {
	return func(config *gormConfig) error {
		config.cacheServerErrors = enabled
		return nil
	}
}

// WithMaxCASAttempts sets the bounded cross-instance contention retry budget.
func WithMaxCASAttempts(attempts int) GORMOption {
	return func(config *gormConfig) error {
		if attempts <= 0 || attempts > maxCASAttempts {
			return fmt.Errorf(
				"%w: max CAS attempts must be between 1 and %d",
				ErrInvalidOption,
				maxCASAttempts,
			)
		}
		config.maxCASAttempts = attempts
		return nil
	}
}

// NewGORM constructs a store without modifying schema. Callers must explicitly
// apply this package's Goose migrations.
func NewGORM(db *gorm.DB, options ...GORMOption) (*GORMStore, error) {
	if db == nil {
		return nil, ErrDatabaseRequired
	}
	config := gormConfig{
		clock:          wallClock{},
		leaseDuration:  defaultLeaseDuration,
		ttl:            defaultTTL,
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
	if config.ttl < config.leaseDuration {
		return nil, fmt.Errorf(
			"%w: TTL must not be shorter than lease duration",
			ErrInvalidOption,
		)
	}
	return &GORMStore{
		db:                db,
		clock:             config.clock,
		leaseDuration:     config.leaseDuration,
		ttl:               config.ttl,
		cacheServerErrors: config.cacheServerErrors,
		maxCASAttempts:    config.maxCASAttempts,
	}, nil
}

// WithDB binds lifecycle operations to another GORM handle, normally the
// caller's business transaction. Completing through the returned store makes
// the response transition commit or roll back with the business write.
func (store *GORMStore) WithDB(db *gorm.DB) (*GORMStore, error) {
	if store == nil || store.db == nil {
		return nil, ErrDatabaseRequired
	}
	if db == nil {
		return nil, ErrDatabaseRequired
	}
	return NewGORM(
		db,
		WithClock(store.clock),
		WithLeaseDuration(store.leaseDuration),
		WithTTL(store.ttl),
		WithCacheServerErrors(store.cacheServerErrors),
		WithMaxCASAttempts(store.maxCASAttempts),
	)
}

// Claim atomically returns one of execute, in-progress, or replay.
func (store *GORMStore) Claim(
	ctx context.Context,
	request ClaimRequest,
) (ClaimResult, error) {
	if ctx == nil {
		return ClaimResult{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	normalized, err := request.normalize()
	if err != nil {
		return ClaimResult{}, err
	}
	if err := store.ready(); err != nil {
		return ClaimResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ClaimResult{}, err
	}

	identity := newIdentity(normalized)
	for attempt := 0; attempt < store.maxCASAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ClaimResult{}, err
		}
		now := wallTime(store.clock.Now())

		var current gormRecord
		queryErr := store.db.WithContext(ctx).
			Where("scope_hash = ?", identity.scopeHash).
			Take(&current).
			Error
		switch {
		case queryErr == nil:
			if err := current.validate(store); err != nil {
				return ClaimResult{}, err
			}
			if !current.matches(identity) {
				return ClaimResult{}, fmt.Errorf(
					"%w: scope hash collision or corrupt identity",
					ErrInvalidState,
				)
			}

			if current.ExpiresAtNS <= now.UnixNano() {
				result, resetErr := store.resetExpired(ctx, current, normalized, now)
				if resetErr != nil {
					return ClaimResult{}, resetErr
				}
				if result != nil {
					return *result, nil
				}
				runtime.Gosched()
				continue
			}
			if current.RequestDigest != normalized.RequestDigest {
				return ClaimResult{}, fmt.Errorf(
					"%w: actor, route, and key already identify another digest",
					ErrConflict,
				)
			}

			switch current.State {
			case stateCompleted:
				response, err := current.response(store)
				if err != nil {
					return ClaimResult{}, err
				}
				return ClaimResult{
					Disposition:     DispositionReplay,
					Response:        &response,
					RecordExpiresAt: unixTime(current.ExpiresAtNS),
				}, nil
			case stateInProgress:
				if current.LeaseExpiresAtNS > now.UnixNano() {
					leaseExpiry := unixTime(current.LeaseExpiresAtNS)
					return ClaimResult{
						Disposition:     DispositionInProgress,
						RetryAfter:      leaseExpiry.Sub(now),
						LeaseExpiresAt:  leaseExpiry,
						RecordExpiresAt: unixTime(current.ExpiresAtNS),
					}, nil
				}
				result, recoveredErr := store.recoverLease(ctx, current, now)
				if recoveredErr != nil {
					return ClaimResult{}, recoveredErr
				}
				if result != nil {
					return *result, nil
				}
			default:
				return ClaimResult{}, fmt.Errorf(
					"%w: unsupported state %q",
					ErrInvalidState,
					current.State,
				)
			}
		case errors.Is(queryErr, gorm.ErrRecordNotFound):
			result, insertErr := store.insertClaim(ctx, normalized, identity, now)
			if insertErr != nil {
				return ClaimResult{}, insertErr
			}
			if result != nil {
				return *result, nil
			}
		default:
			return ClaimResult{}, queryErr
		}
		runtime.Gosched()
	}
	return ClaimResult{}, ErrContention
}

func (store *GORMStore) insertClaim(
	ctx context.Context,
	request ClaimRequest,
	identity requestIdentity,
	now time.Time,
) (*ClaimResult, error) {
	token, err := newLeaseToken()
	if err != nil {
		return nil, err
	}
	leaseExpiry := now.Add(store.leaseDuration)
	recordExpiry := now.Add(store.ttl)
	record := gormRecord{
		ScopeHash:        identity.scopeHash,
		ActorKind:        request.Actor.Kind,
		ActorID:          request.Actor.ID,
		Method:           request.Method,
		Route:            request.Route,
		KeyHash:          identity.keyHash,
		RequestDigest:    request.RequestDigest,
		State:            stateInProgress,
		LeaseTokenHash:   leaseTokenHash(token),
		LeaseExpiresAtNS: leaseExpiry.UnixNano(),
		CreatedAtNS:      now.UnixNano(),
		UpdatedAtNS:      now.UnixNano(),
		ExpiresAtNS:      recordExpiry.UnixNano(),
		Revision:         1,
	}
	result := store.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(&record)
	if result.Error != nil {
		return nil, result.Error
	}

	// MySQL connection settings can make RowsAffected ambiguous for duplicate
	// no-ops. The immutable random token proves which contender inserted.
	var persisted gormRecord
	queryErr := store.db.WithContext(ctx).
		Select("scope_hash", "lease_token_hash").
		Where("scope_hash = ?", identity.scopeHash).
		Take(&persisted).
		Error
	if queryErr != nil {
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, queryErr
	}
	if persisted.LeaseTokenHash != leaseTokenHash(token) {
		return nil, nil
	}
	claim := executionResult(record.ScopeHash, token, leaseExpiry, recordExpiry)
	return &claim, nil
}

func (store *GORMStore) resetExpired(
	ctx context.Context,
	current gormRecord,
	request ClaimRequest,
	now time.Time,
) (*ClaimResult, error) {
	token, err := newLeaseToken()
	if err != nil {
		return nil, err
	}
	leaseExpiry := now.Add(store.leaseDuration)
	recordExpiry := now.Add(store.ttl)
	updated, err := store.compareAndSwap(ctx, current, map[string]any{
		"request_digest":        request.RequestDigest,
		"state":                 stateInProgress,
		"lease_token_hash":      leaseTokenHash(token),
		"lease_expires_at_ns":   leaseExpiry.UnixNano(),
		"response_status":       0,
		"response_content_type": "",
		"response_headers":      nil,
		"response_body":         nil,
		"updated_at_ns":         now.UnixNano(),
		"expires_at_ns":         recordExpiry.UnixNano(),
		"revision":              current.Revision + 1,
	})
	if err != nil || !updated {
		return nil, err
	}
	result := executionResult(current.ScopeHash, token, leaseExpiry, recordExpiry)
	return &result, nil
}

func (store *GORMStore) recoverLease(
	ctx context.Context,
	current gormRecord,
	now time.Time,
) (*ClaimResult, error) {
	token, err := newLeaseToken()
	if err != nil {
		return nil, err
	}
	leaseExpiry := now.Add(store.leaseDuration)
	recordExpiry := now.Add(store.ttl)
	updated, err := store.compareAndSwap(ctx, current, map[string]any{
		"lease_token_hash":    leaseTokenHash(token),
		"lease_expires_at_ns": leaseExpiry.UnixNano(),
		"updated_at_ns":       now.UnixNano(),
		"expires_at_ns":       recordExpiry.UnixNano(),
		"revision":            current.Revision + 1,
	})
	if err != nil || !updated {
		return nil, err
	}
	result := executionResult(current.ScopeHash, token, leaseExpiry, recordExpiry)
	return &result, nil
}

// Renew extends an unexpired lease. Once a lease expires it cannot be renewed,
// because another caller may already have recovered it.
func (store *GORMStore) Renew(ctx context.Context, lease Lease) (Lease, error) {
	if ctx == nil {
		return Lease{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := lease.validate(); err != nil {
		return Lease{}, err
	}
	if err := store.ready(); err != nil {
		return Lease{}, err
	}
	if err := ctx.Err(); err != nil {
		return Lease{}, err
	}
	now := wallTime(store.clock.Now())
	leaseExpiry := now.Add(store.leaseDuration)
	recordExpiry := now.Add(store.ttl)
	result := store.db.WithContext(ctx).
		Model(&gormRecord{}).
		Where(
			"scope_hash = ? AND state = ? AND lease_token_hash = ? AND lease_expires_at_ns > ?",
			lease.ID,
			stateInProgress,
			leaseTokenHash(lease.Token),
			now.UnixNano(),
		).
		Updates(map[string]any{
			"lease_expires_at_ns": leaseExpiry.UnixNano(),
			"updated_at_ns":       now.UnixNano(),
			"expires_at_ns":       recordExpiry.UnixNano(),
			"revision":            gorm.Expr("revision + 1"),
		})
	if result.Error != nil {
		return Lease{}, result.Error
	}
	if result.RowsAffected != 1 {
		return Lease{}, ErrLeaseLost
	}
	return Lease{ID: lease.ID, Token: lease.Token, ExpiresAt: leaseExpiry}, nil
}

// Complete stores a bounded replay response while the execution lease is
// valid. By default, 5xx completion delegates to Abandon and is not retained.
func (store *GORMStore) Complete(
	ctx context.Context,
	lease Lease,
	response Response,
) (CompletionResult, error) {
	if ctx == nil {
		return CompletionResult{}, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := lease.validate(); err != nil {
		return CompletionResult{}, err
	}
	if err := store.ready(); err != nil {
		return CompletionResult{}, err
	}
	if response.Status < http.StatusOK || response.Status > 599 {
		return CompletionResult{}, fmt.Errorf(
			"%w: response status must be between 200 and 599",
			ErrInvalidRequest,
		)
	}
	if response.Status >= 500 && !store.cacheServerErrors {
		if err := store.Abandon(ctx, lease); err != nil {
			return CompletionResult{}, err
		}
		return CompletionResult{Stored: false}, nil
	}
	normalized, err := normalizeResponse(response)
	if err != nil {
		return CompletionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return CompletionResult{}, err
	}

	headers, err := json.Marshal(normalized.Headers)
	if err != nil {
		return CompletionResult{}, fmt.Errorf(
			"%w: encode normalized response headers: %v",
			ErrUnsafeResponse,
			err,
		)
	}
	now := wallTime(store.clock.Now())
	recordExpiry := now.Add(store.ttl)
	result := store.db.WithContext(ctx).
		Model(&gormRecord{}).
		Where(
			"scope_hash = ? AND state = ? AND lease_token_hash = ? AND lease_expires_at_ns > ?",
			lease.ID,
			stateInProgress,
			leaseTokenHash(lease.Token),
			now.UnixNano(),
		).
		Updates(map[string]any{
			"state":                 stateCompleted,
			"lease_token_hash":      "",
			"lease_expires_at_ns":   0,
			"response_status":       normalized.Status,
			"response_content_type": normalized.ContentType,
			"response_headers":      headers,
			"response_body":         normalized.Body,
			"updated_at_ns":         now.UnixNano(),
			"expires_at_ns":         recordExpiry.UnixNano(),
			"revision":              gorm.Expr("revision + 1"),
		})
	if result.Error != nil {
		return CompletionResult{}, result.Error
	}
	if result.RowsAffected != 1 {
		return CompletionResult{}, ErrLeaseLost
	}
	return CompletionResult{Stored: true}, nil
}

// Abandon releases an in-progress claim immediately. It is suitable for
// request failures that should be retried rather than replayed.
func (store *GORMStore) Abandon(ctx context.Context, lease Lease) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := lease.validate(); err != nil {
		return err
	}
	if err := store.ready(); err != nil {
		return err
	}
	result := store.db.WithContext(ctx).
		Where(
			"scope_hash = ? AND state = ? AND lease_token_hash = ?",
			lease.ID,
			stateInProgress,
			leaseTokenHash(lease.Token),
		).
		Delete(&gormRecord{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrLeaseLost
	}
	return nil
}

// CleanupExpired deletes at most batchSize records whose TTL elapsed.
func (store *GORMStore) CleanupExpired(
	ctx context.Context,
	batchSize int,
) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if batchSize <= 0 || batchSize > MaxCleanupBatch {
		return 0, fmt.Errorf(
			"%w: cleanup batch must be between 1 and %d",
			ErrInvalidRequest,
			MaxCleanupBatch,
		)
	}
	if err := store.ready(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	cutoffNS := wallTime(store.clock.Now()).UnixNano()
	var scopeHashes []string
	err := store.db.WithContext(ctx).
		Model(&gormRecord{}).
		Where("expires_at_ns <= ?", cutoffNS).
		Order("expires_at_ns ASC").
		Limit(batchSize).
		Pluck("scope_hash", &scopeHashes).
		Error
	if err != nil || len(scopeHashes) == 0 {
		return 0, err
	}
	result := store.db.WithContext(ctx).
		Where("expires_at_ns <= ? AND scope_hash IN ?", cutoffNS, scopeHashes).
		Delete(&gormRecord{})
	return result.RowsAffected, result.Error
}

func (store *GORMStore) compareAndSwap(
	ctx context.Context,
	current gormRecord,
	updates map[string]any,
) (bool, error) {
	result := store.db.WithContext(ctx).
		Model(&gormRecord{}).
		Where("scope_hash = ? AND revision = ?", current.ScopeHash, current.Revision).
		Updates(updates)
	return result.RowsAffected == 1, result.Error
}

func (store *GORMStore) ready() error {
	if store == nil || store.db == nil || nilInterface(store.clock) {
		return ErrDatabaseRequired
	}
	return nil
}

type gormRecord struct {
	ScopeHash           string      `gorm:"column:scope_hash;primaryKey;size:64"`
	ActorKind           ActorKind   `gorm:"column:actor_kind;size:16;not null"`
	ActorID             string      `gorm:"column:actor_id;size:160;not null"`
	Method              string      `gorm:"column:method;size:16;not null"`
	Route               string      `gorm:"column:route;size:512;not null"`
	KeyHash             string      `gorm:"column:key_hash;size:64;not null"`
	RequestDigest       string      `gorm:"column:request_digest;size:64;not null"`
	State               recordState `gorm:"column:state;size:16;not null"`
	LeaseTokenHash      string      `gorm:"column:lease_token_hash;size:64;not null"`
	LeaseExpiresAtNS    int64       `gorm:"column:lease_expires_at_ns;not null"`
	ResponseStatus      int         `gorm:"column:response_status;not null"`
	ResponseContentType string      `gorm:"column:response_content_type;size:255;not null"`
	ResponseHeaders     []byte      `gorm:"column:response_headers"`
	ResponseBody        []byte      `gorm:"column:response_body"`
	CreatedAtNS         int64       `gorm:"column:created_at_ns;not null"`
	UpdatedAtNS         int64       `gorm:"column:updated_at_ns;not null"`
	ExpiresAtNS         int64       `gorm:"column:expires_at_ns;not null;index"`
	Revision            int64       `gorm:"column:revision;not null"`
}

func (gormRecord) TableName() string {
	return TableName
}

func (record gormRecord) validate(store *GORMStore) error {
	if err := (Actor{Kind: record.ActorKind, ID: record.ActorID}).validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	request := ClaimRequest{
		Actor:         Actor{Kind: record.ActorKind, ID: record.ActorID},
		Method:        record.Method,
		Route:         record.Route,
		Key:           "not-persisted",
		RequestDigest: record.RequestDigest,
	}
	normalized, err := request.normalize()
	if err != nil {
		return fmt.Errorf("%w: invalid persisted request metadata: %v", ErrInvalidState, err)
	}
	if normalized.Method != record.Method {
		return fmt.Errorf("%w: persisted method is not canonical", ErrInvalidState)
	}
	if !validHex(record.ScopeHash, sha256.Size*2) ||
		!validHex(record.KeyHash, sha256.Size*2) ||
		record.CreatedAtNS <= 0 ||
		record.UpdatedAtNS < record.CreatedAtNS ||
		record.ExpiresAtNS < record.UpdatedAtNS ||
		record.Revision <= 0 {
		return fmt.Errorf("%w: corrupt record %q", ErrInvalidState, record.ScopeHash)
	}
	switch record.State {
	case stateInProgress:
		if !validHex(record.LeaseTokenHash, sha256.Size*2) ||
			record.LeaseExpiresAtNS <= 0 ||
			record.ExpiresAtNS < record.LeaseExpiresAtNS ||
			record.ResponseStatus != 0 ||
			record.ResponseContentType != "" ||
			len(record.ResponseHeaders) != 0 ||
			len(record.ResponseBody) != 0 {
			return fmt.Errorf(
				"%w: corrupt in-progress record %q",
				ErrInvalidState,
				record.ScopeHash,
			)
		}
	case stateCompleted:
		if record.LeaseTokenHash != "" ||
			record.LeaseExpiresAtNS != 0 ||
			record.ResponseStatus < 200 ||
			record.ResponseStatus > 599 {
			return fmt.Errorf(
				"%w: corrupt completed record %q",
				ErrInvalidState,
				record.ScopeHash,
			)
		}
		if _, err := record.response(store); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"%w: unsupported state %q",
			ErrInvalidState,
			record.State,
		)
	}
	return nil
}

func (record gormRecord) matches(identity requestIdentity) bool {
	return record.ScopeHash == identity.scopeHash &&
		record.KeyHash == identity.keyHash &&
		record.ScopeHash == scopeHash(
			record.ActorKind,
			record.ActorID,
			record.Method,
			record.Route,
			record.KeyHash,
		)
}

func (record gormRecord) response(store *GORMStore) (Response, error) {
	var headers http.Header
	if err := json.Unmarshal(record.ResponseHeaders, &headers); err != nil {
		return Response{}, fmt.Errorf(
			"%w: decode persisted response headers: %v",
			ErrInvalidState,
			err,
		)
	}
	response, err := normalizeResponse(Response{
		Status:      record.ResponseStatus,
		ContentType: record.ResponseContentType,
		Headers:     headers,
		Body:        record.ResponseBody,
	})
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrInvalidState, err)
	}
	if response.Status >= 500 && !store.cacheServerErrors {
		return Response{}, fmt.Errorf(
			"%w: server error persisted while caching is disabled",
			ErrInvalidState,
		)
	}
	return cloneResponse(response), nil
}

type requestIdentity struct {
	keyHash   string
	scopeHash string
}

func newIdentity(request ClaimRequest) requestIdentity {
	keyDigest := sha256.Sum256([]byte(request.Key))
	keyHash := hex.EncodeToString(keyDigest[:])
	return requestIdentity{
		keyHash: keyHash,
		scopeHash: scopeHash(
			request.Actor.Kind,
			request.Actor.ID,
			request.Method,
			request.Route,
			keyHash,
		),
	}
}

func scopeHash(
	actorKind ActorKind,
	actorID,
	method,
	route,
	keyHash string,
) string {
	hasher := sha256.New()
	for _, value := range []string{
		string(actorKind),
		actorID,
		method,
		route,
		keyHash,
	} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hasher.Write(size[:])
		_, _ = hasher.Write([]byte(value))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func executionResult(
	id,
	token string,
	leaseExpiry,
	recordExpiry time.Time,
) ClaimResult {
	return ClaimResult{
		Disposition: DispositionExecute,
		Lease: Lease{
			ID:        id,
			Token:     token,
			ExpiresAt: leaseExpiry,
		},
		LeaseExpiresAt:  leaseExpiry,
		RecordExpiresAt: recordExpiry,
	}
}

func newLeaseToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate idempotency lease token: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func leaseTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func wallTime(value time.Time) time.Time {
	return time.Unix(0, value.UnixNano()).UTC()
}

func unixTime(nanoseconds int64) time.Time {
	return time.Unix(0, nanoseconds).UTC()
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
