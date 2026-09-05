package filecleanup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	JobType         = "storage.cleanup"
	PayloadVersion1 = uint(1)
	PayloadVersion2 = uint(2)
	PayloadVersion3 = uint(3)
)

// Mode identifies why a version-two cleanup delivery may delete an object.
// Keeping expiry separate from explicit deletion prevents a completed stale
// expiry delivery from suppressing a later user-requested deletion.
type Mode string

const (
	ModeExplicitDelete Mode = "explicit-delete"
	ModePendingExpiry  Mode = "pending-expiry"
)

// PayloadV2 is the exact version-two storage.cleanup payload.
type PayloadV2 struct {
	FileID    string `json:"fileId"`
	Provider  string `json:"provider"`
	ObjectKey string `json:"objectKey"`
	Mode      Mode   `json:"mode"`
}

// HandleV2 dispatches mode-aware cleanup while leaving Handle's version-one
// payload and behavior unchanged.
func (handler *Handler) HandleV2(ctx context.Context, raw json.RawMessage) error {
	if handler == nil {
		return errors.New("file cleanup handler is required")
	}
	job, err := decodePayloadV2(raw)
	if err != nil {
		return err
	}
	if handler.registry == nil && job.Provider != handler.provider {
		return fmt.Errorf("%w: provider %q", ErrObjectChanged, job.Provider)
	}

	switch job.Mode {
	case ModeExplicitDelete:
		legacy, err := json.Marshal(payload{
			FileID:    job.FileID,
			Provider:  job.Provider,
			ObjectKey: job.ObjectKey,
		})
		if err != nil {
			return err
		}
		return handler.Handle(ctx, legacy)
	case ModePendingExpiry:
		return handler.expirePendingUpload(ctx, job)
	default:
		return ErrInvalidPayload
	}
}

func (handler *Handler) expirePendingUpload(ctx context.Context, job PayloadV2) error {
	err := handler.writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var file domain.FileObject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&file, "id = ?", job.FileID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return frameworkaudit.Event{}, errAlreadyCompleted
			}
			return frameworkaudit.Event{}, err
		}
		if file.Provider != job.Provider || file.ObjectKey != job.ObjectKey {
			return frameworkaudit.Event{}, ErrObjectChanged
		}
		if file.Status != "pending" {
			return frameworkaudit.Event{}, errAlreadyCompleted
		}

		before := auditFields(file)
		jobStore, err := handler.storageForFile(&file, "", job.Provider, file.Bucket)
		if err != nil {
			return frameworkaudit.Event{}, err
		}
		if err := jobStore.Delete(ctx, job.ObjectKey); err != nil {
			return frameworkaudit.Event{}, fmt.Errorf("delete expired upload object: %w", err)
		}

		now := handler.clock().UTC()
		file.Status = "deleted"
		file.DeletedAt = &now
		file.UpdatedAt = now
		result := tx.Model(&domain.FileObject{}).
			Where(
				"id = ? AND provider = ? AND object_key = ? AND status = ?",
				file.ID,
				job.Provider,
				job.ObjectKey,
				"pending",
			).
			UpdateColumns(map[string]any{
				"status":     file.Status,
				"deleted_at": file.DeletedAt,
				"updated_at": file.UpdatedAt,
			})
		if result.Error != nil {
			return frameworkaudit.Event{}, result.Error
		}
		if result.RowsAffected != 1 {
			return frameworkaudit.Event{}, fmt.Errorf(
				"%w: pending upload state changed during expiry",
				ErrUnsafeState,
			)
		}

		actorID, actorKind, requestID := handler.auditActor(ctx)
		return frameworkaudit.Event{
			ActorID:    &actorID,
			ActorKind:  actorKind,
			Action:     "files:expire-upload",
			Resource:   "file",
			ResourceID: file.ID,
			Result:     frameworkaudit.ResultSuccess,
			RequestID:  requestID,
			Source:     frameworkaudit.SourceWorker,
			Summary:    "Expired unconfirmed " + file.OriginalName,
			Before:     before,
			After:      auditFields(file),
		}, nil
	})
	if errors.Is(err, errAlreadyCompleted) {
		return nil
	}
	return err
}

func decodePayloadV2(raw json.RawMessage) (PayloadV2, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result PayloadV2
	if err := decoder.Decode(&result); err != nil {
		return PayloadV2{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PayloadV2{}, fmt.Errorf("%w: trailing content", ErrInvalidPayload)
	}

	fileID, err := uuid.Parse(result.FileID)
	if err != nil ||
		fileID.String() != result.FileID ||
		result.Provider == "" ||
		result.Provider != strings.TrimSpace(result.Provider) ||
		len(result.Provider) > 32 ||
		result.ObjectKey == "" ||
		result.ObjectKey != strings.TrimSpace(result.ObjectKey) ||
		len(result.ObjectKey) > 700 ||
		(result.Mode != ModeExplicitDelete && result.Mode != ModePendingExpiry) {
		return PayloadV2{}, ErrInvalidPayload
	}
	if err := storage.ValidateKey(result.ObjectKey); err != nil {
		return PayloadV2{}, fmt.Errorf("%w: object key", ErrInvalidPayload)
	}
	return result, nil
}
