// Package filecleanup implements the durable storage.cleanup job handler.
package filecleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	"github.com/xgtian-root/aginex/backend/framework/authz"
	"github.com/xgtian-root/aginex/backend/framework/jobs"
	"github.com/xgtian-root/aginex/backend/framework/uow"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/auditlog"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidPayload = errors.New("file cleanup: invalid payload")
	ErrUnsafeState    = errors.New("file cleanup: file is not scheduled for deletion")
	ErrObjectChanged  = errors.New("file cleanup: object metadata changed")
)

type Config struct {
	Provider      string
	SystemActorID string
	Clock         func() time.Time
}

type Handler struct {
	db            *gorm.DB
	store         storage.Storage
	registry      *storage.Registry
	writes        *uow.UnitOfWork
	provider      string
	systemActorID string
	clock         func() time.Time
}

func NewWithRegistry(db *gorm.DB, registry *storage.Registry, config Config) (*Handler, error) {
	if db == nil || registry == nil {
		return nil, errors.New("file cleanup database and storage registry are required")
	}
	active, ok := registry.ActiveProfile()
	if !ok {
		return nil, errors.New("file cleanup active storage profile is required")
	}
	store, err := registry.Active()
	if err != nil {
		return nil, err
	}
	config.Provider = active.StorageConfig().Driver
	handler, err := New(db, store, config)
	if err != nil {
		return nil, err
	}
	handler.registry = registry
	return handler, nil
}

func (handler *Handler) storageForFile(file *domain.FileObject, profileID, provider, bucket string) (storage.Storage, error) {
	if handler.registry == nil {
		if provider != handler.provider {
			return nil, fmt.Errorf("%w: provider %q", ErrObjectChanged, provider)
		}
		return handler.store, nil
	}
	if file != nil && file.StorageProfileID != nil && strings.TrimSpace(*file.StorageProfileID) != "" {
		if profileID != "" && profileID != *file.StorageProfileID {
			return nil, ErrObjectChanged
		}
		return handler.registry.Resolve(*file.StorageProfileID)
	}
	if profileID != "" {
		profile, ok := handler.registry.Profile(profileID)
		if !ok || profile.StorageConfig().Driver != provider || profile.StorageConfig().Bucket != bucket {
			return nil, ErrObjectChanged
		}
		return handler.registry.Resolve(profileID)
	}
	if file != nil {
		_, result, err := handler.registry.ResolveLegacy(file.Provider, file.Bucket)
		return result, err
	}
	if bucket != "" {
		_, result, err := handler.registry.ResolveLegacy(provider, bucket)
		return result, err
	}
	_, result, err := handler.registry.ResolveProviderUnique(provider)
	return result, err
}

type payload struct {
	FileID    string `json:"fileId"`
	Provider  string `json:"provider"`
	ObjectKey string `json:"objectKey"`
}

func New(db *gorm.DB, store storage.Storage, config Config) (*Handler, error) {
	if db == nil || store == nil {
		return nil, errors.New("file cleanup database and storage are required")
	}
	config.Provider = strings.TrimSpace(config.Provider)
	if config.Provider == "" {
		return nil, errors.New("file cleanup provider is required")
	}
	config.SystemActorID = strings.TrimSpace(config.SystemActorID)
	if config.SystemActorID == "" {
		config.SystemActorID = "file-cleanup-worker"
	}
	if config.Clock == nil {
		config.Clock = func() time.Time { return time.Now().UTC() }
	}
	writes, err := uow.New(db, auditlog.Recorder{})
	if err != nil {
		return nil, err
	}
	return &Handler{
		db:            db,
		store:         store,
		writes:        writes,
		provider:      config.Provider,
		systemActorID: config.SystemActorID,
		clock:         config.Clock,
	}, nil
}

func (handler *Handler) Handle(ctx context.Context, raw json.RawMessage) error {
	if handler == nil {
		return errors.New("file cleanup handler is required")
	}
	job, err := decodePayload(raw)
	if err != nil {
		return err
	}
	if handler.registry == nil && job.Provider != handler.provider {
		return fmt.Errorf("%w: provider %q", ErrObjectChanged, job.Provider)
	}
	if err := storage.ValidateKey(job.ObjectKey); err != nil {
		return fmt.Errorf("%w: object key", ErrInvalidPayload)
	}

	var file domain.FileObject
	err = handler.db.WithContext(ctx).First(&file, "id = ?", job.FileID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		jobStore, resolveErr := handler.storageForFile(nil, "", job.Provider, "")
		if resolveErr != nil {
			return resolveErr
		}
		return jobStore.Delete(ctx, job.ObjectKey)
	}
	if err != nil {
		return err
	}
	if file.Provider != job.Provider || file.ObjectKey != job.ObjectKey {
		return ErrObjectChanged
	}
	jobStore, err := handler.storageForFile(&file, "", job.Provider, file.Bucket)
	if err != nil {
		return err
	}
	if file.Status == "deleted" {
		return nil
	}
	if file.Status != "deleting" && file.Status != "invalid" && file.Status != "delete_failed" {
		return fmt.Errorf("%w: status %q", ErrUnsafeState, file.Status)
	}
	if err := jobStore.Delete(ctx, job.ObjectKey); err != nil {
		return fmt.Errorf("delete stored object: %w", err)
	}

	now := handler.clock().UTC()
	err = handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var locked domain.FileObject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&locked, "id = ?", job.FileID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return frameworkaudit.Event{}, errMetadataMissing
			}
			return frameworkaudit.Event{}, err
		}
		if locked.Status == "deleted" {
			return frameworkaudit.Event{}, errAlreadyCompleted
		}
		if locked.Provider != job.Provider || locked.ObjectKey != job.ObjectKey {
			return frameworkaudit.Event{}, ErrObjectChanged
		}
		if locked.Status != "deleting" && locked.Status != "invalid" && locked.Status != "delete_failed" {
			return frameworkaudit.Event{}, fmt.Errorf("%w: status %q", ErrUnsafeState, locked.Status)
		}
		before := auditFields(locked)
		locked.Status = "deleted"
		locked.DeletedAt = &now
		locked.UpdatedAt = now
		result := tx.Model(&domain.FileObject{}).
			Where("id = ?", locked.ID).
			UpdateColumns(map[string]any{
				"status":     locked.Status,
				"deleted_at": locked.DeletedAt,
				"updated_at": locked.UpdatedAt,
			})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, errMetadataMissing
		}
		actorID, actorKind, requestID := handler.auditActor(ctx)
		return frameworkaudit.Event{
			ActorID:    &actorID,
			ActorKind:  actorKind,
			Action:     "files:delete-complete",
			Resource:   "file",
			ResourceID: locked.ID,
			Result:     frameworkaudit.ResultSuccess,
			RequestID:  requestID,
			Source:     frameworkaudit.SourceWorker,
			Summary:    "Deleted " + locked.OriginalName,
			Before:     before,
			After:      auditFields(locked),
		}, nil
	})
	if errors.Is(err, errAlreadyCompleted) || errors.Is(err, errMetadataMissing) {
		return nil
	}
	return err
}

var (
	errAlreadyCompleted = errors.New("file cleanup: already completed")
	errMetadataMissing  = errors.New("file cleanup: metadata missing")
)

func (handler *Handler) auditActor(ctx context.Context) (string, string, string) {
	execution, ok := jobs.ExecutionFromContext(ctx)
	if !ok {
		return handler.systemActorID, frameworkaudit.ActorSystem, ""
	}
	kind := frameworkaudit.ActorSystem
	if execution.Actor.Kind == authz.ActorKindUser {
		kind = frameworkaudit.ActorUser
	}
	return execution.Actor.ID, kind, execution.Trace.RequestID
}

func decodePayload(raw json.RawMessage) (payload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result payload
	if err := decoder.Decode(&result); err != nil {
		return payload{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return payload{}, fmt.Errorf("%w: trailing content", ErrInvalidPayload)
	}
	result.FileID = strings.TrimSpace(result.FileID)
	result.Provider = strings.TrimSpace(result.Provider)
	result.ObjectKey = strings.TrimSpace(result.ObjectKey)
	fileID, err := uuid.Parse(result.FileID)
	if err != nil ||
		fileID.String() != result.FileID ||
		result.Provider == "" ||
		result.ObjectKey == "" {
		return payload{}, ErrInvalidPayload
	}
	return result, nil
}

func auditFields(file domain.FileObject) map[string]any {
	fields := map[string]any{
		"id":           file.ID,
		"originalName": file.OriginalName,
		"contentType":  file.ContentType,
		"size":         file.Size,
		"sha256":       file.SHA256,
		"status":       file.Status,
	}
	if file.DeletedAt != nil {
		fields["deletedAt"] = file.DeletedAt.UTC()
	}
	return fields
}
