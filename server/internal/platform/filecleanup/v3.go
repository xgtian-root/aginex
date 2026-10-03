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
	"github.com/xgtian-root/aginex/server/internal/platform/storage"
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
	if handler == nil {
		return errors.New("file cleanup handler is required")
	}
	if handler.registry == nil && job.Provider != handler.provider {
		return fmt.Errorf("%w: provider %q", ErrObjectChanged, job.Provider)
	}
	if job.Mode == ModePendingExpiry {
		return handler.expirePendingUpload(ctx, job)
	}
	return handler.deleteFile(ctx, job)
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
