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
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
)

type PayloadV3 struct {
	FileID    string `json:"fileId"`
	ProfileID string `json:"profileId"`
	Provider  string `json:"provider"`
	Bucket    string `json:"bucket"`
	ObjectKey string `json:"objectKey"`
	Mode      Mode   `json:"mode"`
}

func (handler *Handler) HandleV3(ctx context.Context, raw json.RawMessage) error {
	job, err := decodePayloadV3(raw)
	if err != nil {
		return err
	}
	var file domain.FileObject
	err = handler.db.WithContext(ctx).First(&file, "id = ?", job.FileID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if job.Mode == ModePendingExpiry {
			return nil
		}
		jobStore, resolveErr := handler.storageForFile(nil, job.ProfileID, job.Provider, job.Bucket)
		if resolveErr != nil {
			return resolveErr
		}
		return jobStore.Delete(ctx, job.ObjectKey)
	}
	if err != nil {
		return err
	}
	if file.StorageProfileID == nil || *file.StorageProfileID != job.ProfileID ||
		file.Provider != job.Provider || file.Bucket != job.Bucket || file.ObjectKey != job.ObjectKey {
		return ErrObjectChanged
	}
	legacy, err := json.Marshal(PayloadV2{
		FileID: job.FileID, Provider: job.Provider, ObjectKey: job.ObjectKey, Mode: job.Mode,
	})
	if err != nil {
		return err
	}
	return handler.HandleV2(ctx, legacy)
}

func decodePayloadV3(raw json.RawMessage) (PayloadV3, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var result PayloadV3
	if err := decoder.Decode(&result); err != nil {
		return PayloadV3{}, fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PayloadV3{}, fmt.Errorf("%w: trailing content", ErrInvalidPayload)
	}
	fileID, fileErr := uuid.Parse(result.FileID)
	profileID, profileErr := uuid.Parse(result.ProfileID)
	if fileErr != nil || fileID.String() != result.FileID ||
		profileErr != nil || profileID.String() != result.ProfileID ||
		result.Provider == "" || result.Provider != strings.TrimSpace(result.Provider) ||
		result.Bucket != strings.TrimSpace(result.Bucket) ||
		result.ObjectKey == "" || result.ObjectKey != strings.TrimSpace(result.ObjectKey) ||
		(result.Mode != ModeExplicitDelete && result.Mode != ModePendingExpiry) {
		return PayloadV3{}, ErrInvalidPayload
	}
	if err := storage.ValidateKey(result.ObjectKey); err != nil {
		return PayloadV3{}, fmt.Errorf("%w: object key", ErrInvalidPayload)
	}
	return result, nil
}
