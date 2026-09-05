// Package postgres provides the opt-in PostgreSQL durable job queue.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/jobs"
	"gorm.io/gorm"
)

const jobColumns = `
	id, type, version, payload, payload_sha256, idempotency_key, state,
	scheduled_at, attempts, max_attempts, locked_by, locked_at, heartbeat_at,
	last_error, created_by_kind, created_by_id, request_id, traceparent,
	created_at, updated_at, completed_at
`

const updatedJobColumns = `
	job.id, job.type, job.version, job.payload, job.payload_sha256,
	job.idempotency_key, job.state, job.scheduled_at, job.attempts,
	job.max_attempts, job.locked_by, job.locked_at, job.heartbeat_at,
	job.last_error, job.created_by_kind, job.created_by_id, job.request_id,
	job.traceparent, job.created_at, job.updated_at, job.completed_at
`

const storedFailureSummary = "job execution failed"

type Config struct {
	LeaseDuration time.Duration
	BaseRetry     time.Duration
	MaxRetry      time.Duration
	Clock         func() time.Time
	Jitter        func() float64
}

type Store struct {
	db            *gorm.DB
	leaseDuration time.Duration
	baseRetry     time.Duration
	maxRetry      time.Duration
	clock         func() time.Time
	jitter        func() float64
}

var _ jobs.Queue = (*Store)(nil)
var _ jobs.TransactionalQueue = (*Store)(nil)
var _ jobs.Inspector = (*Store)(nil)
var _ jobs.AdministrativeQueue = (*Store)(nil)

func New(db *gorm.DB, config Config) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("%w: database is required", jobs.ErrInvalid)
	}
	if db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return nil, fmt.Errorf("%w: PostgreSQL job store requires the postgres dialector", jobs.ErrInvalid)
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = time.Minute
	}
	if config.BaseRetry <= 0 {
		config.BaseRetry = time.Second
	}
	if config.MaxRetry <= 0 {
		config.MaxRetry = 15 * time.Minute
	}
	if config.MaxRetry < config.BaseRetry {
		return nil, fmt.Errorf("%w: maximum retry must not be shorter than base retry", jobs.ErrInvalid)
	}
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	if config.Jitter == nil {
		config.Jitter = func() float64 { return rand.Float64()*2 - 1 }
	}
	return &Store{
		db:            db,
		leaseDuration: config.LeaseDuration,
		baseRetry:     config.BaseRetry,
		maxRetry:      config.MaxRetry,
		clock:         config.Clock,
		jitter:        config.Jitter,
	}, nil
}

// WithDB binds the queue to a GORM transaction so enqueue and business writes
// can commit atomically.
func (store *Store) WithDB(db *gorm.DB) (*Store, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: store is required", jobs.ErrInvalid)
	}
	bound, err := New(db, Config{
		LeaseDuration: store.leaseDuration,
		BaseRetry:     store.baseRetry,
		MaxRetry:      store.maxRetry,
		Clock:         store.clock,
		Jitter:        store.jitter,
	})
	if err != nil {
		return nil, err
	}
	return bound, nil
}

// Bind implements jobs.TransactionalQueue.
func (store *Store) Bind(db *gorm.DB) (jobs.Queue, error) {
	return store.WithDB(db)
}

func (store *Store) Enqueue(
	ctx context.Context,
	request jobs.EnqueueRequest,
) (jobs.EnqueueResult, error) {
	now := store.clock().UTC()
	if err := request.Normalize(now); err != nil {
		return jobs.EnqueueResult{}, err
	}
	payload, hash, err := normalizePayload(request.Payload)
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	id := uuid.NewString()
	query := `
		INSERT INTO aginex_jobs (
			id, type, version, payload, payload_sha256, idempotency_key, state,
			scheduled_at, attempts, max_attempts, created_by_kind, created_by_id,
			request_id, traceparent, created_at, updated_at
		) VALUES (
			?, ?, ?, ?::jsonb, ?, ?, 'pending',
			?, 0, ?, ?, ?, ?, ?, ?, ?
		)
		ON CONFLICT (type, idempotency_key) DO NOTHING
		RETURNING ` + jobColumns
	created, err := scanJob(store.db.WithContext(ctx).Raw(
		query,
		id,
		request.Type,
		request.Version,
		payload,
		hash,
		request.IdempotencyKey,
		request.ScheduledAt,
		request.MaxAttempts,
		request.CreatedBy.Kind,
		request.CreatedBy.ID,
		request.Trace.RequestID,
		request.Trace.TraceParent,
		now,
		now,
	).Row())
	if err == nil {
		return jobs.EnqueueResult{Job: created, Created: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return jobs.EnqueueResult{}, fmt.Errorf("enqueue job: %w", err)
	}

	existing, err := store.jobByTypeAndKey(ctx, request.Type, request.IdempotencyKey)
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	if existing.Version != request.Version || existing.PayloadSHA256 != hash {
		return jobs.EnqueueResult{}, fmt.Errorf(
			"%w: key %q already belongs to a different request",
			jobs.ErrIdempotencyConflict,
			request.IdempotencyKey,
		)
	}
	return jobs.EnqueueResult{Job: existing, Created: false}, nil
}

func (store *Store) Claim(
	ctx context.Context,
	request jobs.ClaimRequest,
) ([]jobs.Job, error) {
	normalized, err := jobs.NormalizeClaim(request)
	if err != nil {
		return nil, err
	}
	now := store.clock().UTC()
	staleBefore := now.Add(-store.leaseDuration)
	var claimed []jobs.Job
	err = store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reap := tx.Exec(`
			UPDATE aginex_jobs
			SET state = 'dead',
			    locked_by = NULL,
			    locked_at = NULL,
			    heartbeat_at = NULL,
			    completed_at = ?,
			    updated_at = ?,
			    last_error = CASE
			        WHEN last_error = '' THEN 'worker lease expired after maximum attempts'
			        ELSE last_error
			    END
			WHERE state = 'running'
			  AND COALESCE(heartbeat_at, locked_at) <= ?
			  AND attempts >= max_attempts
		`, now, now, staleBefore)
		if reap.Error != nil {
			return fmt.Errorf("reap exhausted jobs: %w", reap.Error)
		}

		rows, err := tx.Raw(`
			WITH candidates AS (
				SELECT id
				FROM aginex_jobs
				WHERE (
					state IN ('pending', 'failed')
					AND scheduled_at <= ?
				) OR (
					state = 'running'
					AND COALESCE(heartbeat_at, locked_at) <= ?
					AND attempts < max_attempts
				)
				ORDER BY scheduled_at, created_at, id
				FOR UPDATE SKIP LOCKED
				LIMIT ?
			)
			UPDATE aginex_jobs AS job
			SET state = 'running',
			    attempts = job.attempts + 1,
			    locked_by = ?,
			    locked_at = ?,
			    heartbeat_at = ?,
			    completed_at = NULL,
			    updated_at = ?
			FROM candidates
			WHERE job.id = candidates.id
			RETURNING `+updatedJobColumns,
			now,
			staleBefore,
			normalized.Limit,
			normalized.WorkerID,
			now,
			now,
			now,
		).Rows()
		if err != nil {
			return fmt.Errorf("claim jobs: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			job, err := scanJob(rows)
			if err != nil {
				return fmt.Errorf("scan claimed job: %w", err)
			}
			claimed = append(claimed, job)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read claimed jobs: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if claimed == nil {
		claimed = []jobs.Job{}
	}
	return claimed, nil
}

func (store *Store) Heartbeat(ctx context.Context, id, workerID string) error {
	if _, err := jobs.NormalizeClaim(jobs.ClaimRequest{WorkerID: workerID, Limit: 1}); err != nil {
		return err
	}
	now := store.clock().UTC()
	result := store.db.WithContext(ctx).Exec(`
		UPDATE aginex_jobs
		SET heartbeat_at = ?, updated_at = ?
		WHERE id = ? AND state = 'running' AND locked_by = ?
	`, now, now, strings.TrimSpace(id), workerID)
	return leaseResult("heartbeat", result)
}

func (store *Store) Succeed(ctx context.Context, id, workerID string) error {
	if _, err := jobs.NormalizeClaim(jobs.ClaimRequest{WorkerID: workerID, Limit: 1}); err != nil {
		return err
	}
	now := store.clock().UTC()
	result := store.db.WithContext(ctx).Exec(`
		UPDATE aginex_jobs
		SET state = 'succeeded',
		    locked_by = NULL,
		    locked_at = NULL,
		    heartbeat_at = NULL,
		    last_error = '',
		    completed_at = ?,
		    updated_at = ?
		WHERE id = ? AND state = 'running' AND locked_by = ?
	`, now, now, strings.TrimSpace(id), workerID)
	return leaseResult("complete", result)
}

func (store *Store) Fail(
	ctx context.Context,
	id,
	workerID string,
	jobError error,
) (jobs.State, error) {
	if _, err := jobs.NormalizeClaim(jobs.ClaimRequest{WorkerID: workerID, Limit: 1}); err != nil {
		return "", err
	}
	if jobError == nil {
		return "", fmt.Errorf("%w: failure error is required", jobs.ErrInvalid)
	}
	now := store.clock().UTC()
	var state jobs.State
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var attempts, maxAttempts int
		row := tx.Raw(`
			SELECT attempts, max_attempts
			FROM aginex_jobs
			WHERE id = ? AND state = 'running' AND locked_by = ?
			FOR UPDATE
		`, strings.TrimSpace(id), workerID).Row()
		if err := row.Scan(&attempts, &maxAttempts); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return jobs.ErrLeaseLost
			}
			return fmt.Errorf("read failed job: %w", err)
		}

		state = jobs.StateFailed
		scheduledAt := now.Add(jobs.RetryDelay(
			attempts,
			store.baseRetry,
			store.maxRetry,
			store.jitter(),
		))
		var completedAt *time.Time
		if attempts >= maxAttempts {
			state = jobs.StateDead
			completedAt = &now
			scheduledAt = now
		}
		result := tx.Exec(`
			UPDATE aginex_jobs
			SET state = ?,
			    scheduled_at = ?,
			    locked_by = NULL,
			    locked_at = NULL,
			    heartbeat_at = NULL,
			    last_error = ?,
			    completed_at = ?,
			    updated_at = ?
			WHERE id = ? AND state = 'running' AND locked_by = ?
		`,
			state,
			scheduledAt,
			safeFailureSummary(jobError),
			completedAt,
			now,
			strings.TrimSpace(id),
			workerID,
		)
		return leaseResult("fail", result)
	})
	if err != nil {
		return "", err
	}
	return state, nil
}

func (store *Store) List(
	ctx context.Context,
	request jobs.ListRequest,
) (jobs.ListResult, error) {
	normalized, err := jobs.NormalizeList(request)
	if err != nil {
		return jobs.ListResult{}, err
	}
	where := "state = ?"
	args := []any{normalized.State}
	if normalized.Type != "" {
		where += " AND type = ?"
		args = append(args, normalized.Type)
	}

	var total int64
	if err := store.db.WithContext(ctx).Raw(
		"SELECT COUNT(*) FROM aginex_jobs WHERE "+where,
		args...,
	).Row().Scan(&total); err != nil {
		return jobs.ListResult{}, fmt.Errorf("count jobs: %w", err)
	}

	queryArgs := append(
		append([]any(nil), args...),
		normalized.PageSize,
		(normalized.Page-1)*normalized.PageSize,
	)
	rows, err := store.db.WithContext(ctx).Raw(
		"SELECT "+jobColumns+
			" FROM aginex_jobs WHERE "+where+
			" ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?",
		queryArgs...,
	).Rows()
	if err != nil {
		return jobs.ListResult{}, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	items := make([]jobs.Job, 0, normalized.PageSize)
	for rows.Next() {
		item, err := scanJob(rows)
		if err != nil {
			return jobs.ListResult{}, fmt.Errorf("scan listed job: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return jobs.ListResult{}, fmt.Errorf("read listed jobs: %w", err)
	}
	return jobs.ListResult{
		Jobs:     items,
		Page:     normalized.Page,
		PageSize: normalized.PageSize,
		Total:    total,
	}, nil
}

func (store *Store) RetryDead(ctx context.Context, id string) error {
	normalizedID, err := jobs.NormalizeJobID(id)
	if err != nil {
		return err
	}
	now := store.clock().UTC()
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var state jobs.State
		err := tx.Raw(
			"SELECT state FROM aginex_jobs WHERE id = ? FOR UPDATE",
			normalizedID,
		).Row().Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return jobs.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read dead job: %w", err)
		}
		if state != jobs.StateDead {
			return fmt.Errorf(
				"%w: job is %s, want %s",
				jobs.ErrInvalidTransition,
				state,
				jobs.StateDead,
			)
		}
		result := tx.Exec(`
			UPDATE aginex_jobs
			SET state = 'pending',
			    scheduled_at = ?,
			    attempts = 0,
			    locked_by = NULL,
			    locked_at = NULL,
			    heartbeat_at = NULL,
			    last_error = '',
			    completed_at = NULL,
			    updated_at = ?
			WHERE id = ? AND state = 'dead'
		`, now, now, normalizedID)
		if result.Error != nil {
			return fmt.Errorf("retry dead job: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return jobs.ErrInvalidTransition
		}
		return nil
	})
}

func (store *Store) jobByTypeAndKey(
	ctx context.Context,
	jobType,
	key string,
) (jobs.Job, error) {
	job, err := scanJob(store.db.WithContext(ctx).Raw(`
		SELECT `+jobColumns+`
		FROM aginex_jobs
		WHERE type = ? AND idempotency_key = ?
	`, jobType, key).Row())
	if errors.Is(err, sql.ErrNoRows) {
		return jobs.Job{}, jobs.ErrNotFound
	}
	if err != nil {
		return jobs.Job{}, fmt.Errorf("read idempotent job: %w", err)
	}
	return job, nil
}

type scanner interface {
	Scan(...any) error
}

func scanJob(row scanner) (jobs.Job, error) {
	var job jobs.Job
	var actorKind authz.ActorKind
	var payload []byte
	var lockedBy sql.NullString
	var lockedAt, heartbeatAt, completedAt sql.NullTime
	err := row.Scan(
		&job.ID,
		&job.Type,
		&job.Version,
		&payload,
		&job.PayloadSHA256,
		&job.IdempotencyKey,
		&job.State,
		&job.ScheduledAt,
		&job.Attempts,
		&job.MaxAttempts,
		&lockedBy,
		&lockedAt,
		&heartbeatAt,
		&job.LastError,
		&actorKind,
		&job.CreatedBy.ID,
		&job.Trace.RequestID,
		&job.Trace.TraceParent,
		&job.CreatedAt,
		&job.UpdatedAt,
		&completedAt,
	)
	if err != nil {
		return jobs.Job{}, err
	}
	job.Payload = append(job.Payload[:0], payload...)
	job.CreatedBy.Kind = actorKind
	if lockedBy.Valid {
		job.LockedBy = lockedBy.String
	}
	if lockedAt.Valid {
		value := lockedAt.Time.UTC()
		job.LockedAt = &value
	}
	if heartbeatAt.Valid {
		value := heartbeatAt.Time.UTC()
		job.HeartbeatAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		job.CompletedAt = &value
	}
	job.ScheduledAt = job.ScheduledAt.UTC()
	job.CreatedAt = job.CreatedAt.UTC()
	job.UpdatedAt = job.UpdatedAt.UTC()
	return job, nil
}

func normalizePayload(payload []byte) ([]byte, string, error) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return nil, "", fmt.Errorf("%w: payload must be valid JSON", jobs.ErrInvalid)
	}
	sum := sha256.Sum256(compact.Bytes())
	return compact.Bytes(), hex.EncodeToString(sum[:]), nil
}

func safeFailureSummary(_ error) string {
	// Provider, handler, and driver errors are opaque and may contain
	// credentials, signed URLs, payload fragments, invalid UTF-8, or terminal
	// control sequences. Persist a fixed operator-safe summary; detailed errors
	// belong in access-controlled, redacted telemetry rather than the durable
	// queue row.
	return storedFailureSummary
}

func leaseResult(action string, result *gorm.DB) error {
	if result.Error != nil {
		return fmt.Errorf("%s job lease: %w", action, result.Error)
	}
	if result.RowsAffected != 1 {
		return jobs.ErrLeaseLost
	}
	return nil
}
