package auditlog

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/framework/audit"
	"github.com/xgtian-root/aginex/internal/domain"
	"gorm.io/gorm"
)

type Recorder struct{}

func (Recorder) Record(ctx context.Context, tx *gorm.DB, event audit.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	before, err := audit.NewSanitizedFields(map[string]any(event.Before))
	if err != nil {
		return err
	}
	after, err := audit.NewSanitizedFields(map[string]any(event.After))
	if err != nil {
		return err
	}
	entry := domain.AuditLog{
		ID:         uuid.NewString(),
		ActorID:    event.ActorID,
		ActorKind:  event.ActorKind,
		Action:     event.Action,
		Resource:   event.Resource,
		ResourceID: event.ResourceID,
		Result:     event.Result,
		Source:     event.Source,
		Summary:    event.Summary,
		RequestID:  event.RequestID,
		IPAddress:  event.IPAddress,
		Before:     before,
		After:      after,
		CreatedAt:  time.Now().UTC(),
	}
	return tx.WithContext(ctx).Create(&entry).Error
}
