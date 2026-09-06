// Package multipartcleanup repairs abandoned resumable upload sessions without
// exposing provider upload identifiers to jobs, logs, audits, or HTTP clients.
package multipartcleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	frameworkaudit "github.com/xgtian-root/aginex/server/framework/audit"
	"github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/framework/jobs"
	frameworkstorage "github.com/xgtian-root/aginex/server/framework/storage"
	"github.com/xgtian-root/aginex/server/framework/uow"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/auditlog"
	platformstorage "github.com/xgtian-root/aginex/server/internal/platform/storage"
	"gorm.io/gorm"
)

var (
	ErrUnsafeState   = errors.New("multipart cleanup: upload session state is unsafe")
	ErrObjectChanged = errors.New("multipart cleanup: storage metadata changed")
	errAlreadyDone   = errors.New("multipart cleanup: already completed")
	errStateChanged  = errors.New("multipart cleanup: state changed")
)

type Config struct {
	SystemActorID string
	Clock         func() time.Time
}

type Handler struct {
	db            *gorm.DB
	registry      *platformstorage.Registry
	writes        *uow.UnitOfWork
	systemActorID string
	clock         func() time.Time
}

func New(
	db *gorm.DB,
	registry *platformstorage.Registry,
	config Config,
) (*Handler, error) {
	if db == nil || registry == nil {
		return nil, errors.New("multipart cleanup database and storage registry are required")
	}
	config.SystemActorID = strings.TrimSpace(config.SystemActorID)
	if config.SystemActorID == "" {
		config.SystemActorID = "multipart-cleanup-worker"
	}
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	writes, err := uow.New(db, auditlog.Recorder{})
	if err != nil {
		return nil, err
	}
	return &Handler{
		db: db, registry: registry, writes: writes,
		systemActorID: config.SystemActorID,
		clock:         config.Clock,
	}, nil
}

func (handler *Handler) Handle(ctx context.Context, raw json.RawMessage) error {
	if handler == nil {
		return errors.New("multipart cleanup handler is required")
	}
	payload, err := decodePayload(raw)
	if err != nil {
		return err
	}
	return handler.Cleanup(ctx, payload.SessionID, payload.Cause)
}

// Cleanup advances one abandoned upload through an audited intermediate state,
// aborts the exact multipart upload from its creation-time storage profile,
// and then commits the terminal session and file state. Repeated calls are
// idempotent and a completion that already won the CAS race is never aborted.
func (handler *Handler) Cleanup(
	ctx context.Context,
	sessionID string,
	cause Cause,
) error {
	if handler == nil {
		return errors.New("multipart cleanup handler is required")
	}
	if err := (Payload{SessionID: sessionID, Cause: cause}).Validate(); err != nil {
		return err
	}

	file, session, err := handler.load(ctx, sessionID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if cleanupAlreadyDone(file, session) {
		return nil
	}
	intermediate, terminal, proceed, err := cleanupStates(session, cause, handler.clock().UTC())
	if err != nil || !proceed {
		return err
	}
	if session.Status != intermediate {
		if err := handler.begin(ctx, file, session, cause, intermediate); err != nil {
			if errors.Is(err, errAlreadyDone) || errors.Is(err, errStateChanged) {
				return handler.retryAfterRace(ctx, sessionID, cause)
			}
			return err
		}
		session.Status = intermediate
	}

	multipartStore, upload, err := handler.multipartStore(file, session)
	if err != nil {
		return err
	}
	if err := multipartStore.AbortMultipart(ctx, upload); err != nil {
		return fmt.Errorf("abort multipart upload: %w", err)
	}
	if err := handler.finish(ctx, sessionID, cause, intermediate, terminal); err != nil {
		if errors.Is(err, errAlreadyDone) {
			return nil
		}
		return err
	}
	return nil
}

func (handler *Handler) retryAfterRace(
	ctx context.Context,
	sessionID string,
	cause Cause,
) error {
	file, session, err := handler.load(ctx, sessionID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && cleanupAlreadyDone(file, session)) {
		return nil
	}
	if err != nil {
		return err
	}
	_, _, proceed, stateErr := cleanupStates(session, cause, handler.clock().UTC())
	if stateErr != nil || !proceed {
		return stateErr
	}
	return handler.Cleanup(ctx, sessionID, cause)
}

func (handler *Handler) begin(
	ctx context.Context,
	file domain.FileObject,
	session domain.FileUploadSession,
	cause Cause,
	intermediate domain.FileUploadSessionStatus,
) error {
	return handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var current domain.FileUploadSession
		if err := tx.First(&current, "id = ?", session.ID).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		var currentFile domain.FileObject
		if err := tx.First(&currentFile, "id = ?", current.FileID).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if cleanupAlreadyDone(currentFile, current) {
			return frameworkaudit.Event{}, errAlreadyDone
		}
		if current.Status != domain.FileUploadSessionStatusActive {
			return frameworkaudit.Event{}, errStateChanged
		}
		if current.FileID != file.ID || currentFile.ObjectKey != file.ObjectKey {
			return frameworkaudit.Event{}, ErrObjectChanged
		}
		before := cleanupAuditFields(currentFile, current, cause)
		now := handler.clock().UTC()
		result := tx.Model(&domain.FileUploadSession{}).
			Where("id = ? AND status = ?", current.ID, domain.FileUploadSessionStatusActive).
			UpdateColumns(map[string]any{"status": intermediate, "updated_at": now})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errStateChanged
		}
		current.Status = intermediate
		current.UpdatedAt = now
		return handler.auditEvent(ctx, current, cause, "start", before, cleanupAuditFields(currentFile, current, cause)), nil
	})
}

func (handler *Handler) finish(
	ctx context.Context,
	sessionID string,
	cause Cause,
	intermediate domain.FileUploadSessionStatus,
	terminal domain.FileUploadSessionStatus,
) error {
	return handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var session domain.FileUploadSession
		if err := tx.First(&session, "id = ?", sessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return frameworkaudit.Event{}, errAlreadyDone
			}
			return frameworkaudit.Event{}, err
		}
		var file domain.FileObject
		if err := tx.First(&file, "id = ?", session.FileID).Error; err != nil {
			return frameworkaudit.Event{}, err
		}
		if cleanupAlreadyDone(file, session) {
			return frameworkaudit.Event{}, errAlreadyDone
		}
		if session.Status != intermediate {
			return frameworkaudit.Event{}, errStateChanged
		}
		if file.Status != "pending" && file.Status != "invalid" && file.Status != "deleted" {
			return frameworkaudit.Event{}, fmt.Errorf("%w: file status %q", ErrUnsafeState, file.Status)
		}
		before := cleanupAuditFields(file, session, cause)
		now := handler.clock().UTC()
		result := tx.Model(&domain.FileUploadSession{}).
			Where("id = ? AND status = ?", session.ID, intermediate).
			UpdateColumns(map[string]any{"status": terminal, "updated_at": now})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errStateChanged
		}
		session.Status = terminal
		session.UpdatedAt = now
		if file.Status != "deleted" {
			result = tx.Model(&domain.FileObject{}).
				Where("id = ? AND status IN ?", file.ID, []string{"pending", "invalid"}).
				UpdateColumns(map[string]any{"status": "deleted", "deleted_at": now, "updated_at": now})
			if result.Error != nil {
				return frameworkaudit.Event{}, result.Error
			}
			if result.RowsAffected != 1 {
				return frameworkaudit.Event{}, errStateChanged
			}
			file.Status = "deleted"
			file.DeletedAt = &now
			file.UpdatedAt = now
		}
		return handler.auditEvent(ctx, session, cause, "complete", before, cleanupAuditFields(file, session, cause)), nil
	})
}

func (handler *Handler) load(
	ctx context.Context,
	sessionID string,
) (domain.FileObject, domain.FileUploadSession, error) {
	var session domain.FileUploadSession
	if err := handler.db.WithContext(ctx).First(&session, "id = ?", sessionID).Error; err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}
	var file domain.FileObject
	if err := handler.db.WithContext(ctx).First(&file, "id = ?", session.FileID).Error; err != nil {
		return domain.FileObject{}, domain.FileUploadSession{}, err
	}
	return file, session, nil
}

func (handler *Handler) multipartStore(
	file domain.FileObject,
	session domain.FileUploadSession,
) (frameworkstorage.MultipartObjectStore, frameworkstorage.MultipartUpload, error) {
	if file.StorageProfileID == nil || strings.TrimSpace(*file.StorageProfileID) == "" ||
		strings.TrimSpace(file.ObjectKey) == "" || strings.TrimSpace(session.ProviderUploadID) == "" {
		return nil, frameworkstorage.MultipartUpload{}, ErrObjectChanged
	}
	if err := platformstorage.ValidateKey(file.ObjectKey); err != nil {
		return nil, frameworkstorage.MultipartUpload{}, ErrObjectChanged
	}
	profile, ok := handler.registry.Profile(*file.StorageProfileID)
	if !ok {
		return nil, frameworkstorage.MultipartUpload{}, platformstorage.ErrStorageProfileNotFound
	}
	storageConfig := profile.StorageConfig()
	if storageConfig.Driver != file.Provider || storageConfig.Bucket != file.Bucket {
		return nil, frameworkstorage.MultipartUpload{}, ErrObjectChanged
	}
	store, err := handler.registry.ResolveMultipart(*file.StorageProfileID)
	if err != nil {
		return nil, frameworkstorage.MultipartUpload{}, err
	}
	return store, frameworkstorage.MultipartUpload{
		Key: file.ObjectKey, ProviderUploadID: session.ProviderUploadID,
	}, nil
}

func cleanupStates(
	session domain.FileUploadSession,
	cause Cause,
	now time.Time,
) (
	domain.FileUploadSessionStatus,
	domain.FileUploadSessionStatus,
	bool,
	error,
) {
	switch cause {
	case CauseExpiry:
		switch session.Status {
		case domain.FileUploadSessionStatusActive:
			if session.ExpiresAt.After(now) {
				return "", "", false, fmt.Errorf("%w: session has not expired", ErrUnsafeState)
			}
			return domain.FileUploadSessionStatusExpiring, domain.FileUploadSessionStatusExpired, true, nil
		case domain.FileUploadSessionStatusExpiring:
			return domain.FileUploadSessionStatusExpiring, domain.FileUploadSessionStatusExpired, true, nil
		default:
			return "", "", false, nil
		}
	case CauseCancel:
		switch session.Status {
		case domain.FileUploadSessionStatusActive:
			return domain.FileUploadSessionStatusCancelling, domain.FileUploadSessionStatusCancelled, true, nil
		case domain.FileUploadSessionStatusCancelling:
			return domain.FileUploadSessionStatusCancelling, domain.FileUploadSessionStatusCancelled, true, nil
		default:
			return "", "", false, nil
		}
	default:
		return "", "", false, ErrInvalidPayload
	}
}

func cleanupAlreadyDone(file domain.FileObject, session domain.FileUploadSession) bool {
	if file.Status == "ready" || session.Status == domain.FileUploadSessionStatusCompleted {
		return true
	}
	return session.Status == domain.FileUploadSessionStatusCancelled ||
		session.Status == domain.FileUploadSessionStatusExpired
}

func (handler *Handler) auditEvent(
	ctx context.Context,
	session domain.FileUploadSession,
	cause Cause,
	phase string,
	before frameworkaudit.SanitizedFields,
	after frameworkaudit.SanitizedFields,
) frameworkaudit.Event {
	actorID, actorKind, requestID, source := handler.auditActor(ctx)
	action := "files:expire-upload-session"
	summary := "Expired abandoned resumable upload"
	if cause == CauseCancel {
		action = "files:cancel-upload-session"
		summary = "Cancelled resumable upload"
	}
	if phase == "start" {
		action += "-start"
		summary = "Started resumable upload cleanup"
	}
	return frameworkaudit.Event{
		ActorID: &actorID, ActorKind: actorKind,
		Action: action, Resource: "file-upload-session", ResourceID: session.ID,
		Result: frameworkaudit.ResultSuccess, RequestID: requestID, Source: source,
		Summary: summary, Before: before, After: after,
	}
}

func (handler *Handler) auditActor(ctx context.Context) (string, string, string, string) {
	execution, ok := jobs.ExecutionFromContext(ctx)
	if !ok {
		return handler.systemActorID, frameworkaudit.ActorSystem, "", frameworkaudit.SourceSystem
	}
	actorKind := frameworkaudit.ActorSystem
	if execution.Actor.Kind == authz.ActorKindUser {
		actorKind = frameworkaudit.ActorUser
	}
	return execution.Actor.ID, actorKind, execution.Trace.RequestID, frameworkaudit.SourceWorker
}

func cleanupAuditFields(
	file domain.FileObject,
	session domain.FileUploadSession,
	cause Cause,
) frameworkaudit.SanitizedFields {
	return frameworkaudit.SanitizedFields{
		"id": session.ID, "fileId": file.ID,
		"status": session.Status, "fileStatus": file.Status,
		"cause": cause, "expiresAt": session.ExpiresAt.UTC(),
	}
}
