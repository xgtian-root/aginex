package app

import (
	"context"

	frameworkaudit "github.com/xgtian-root/aginex/backend/framework/audit"
	"github.com/xgtian-root/aginex/backend/framework/uow"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/storage"
	"gorm.io/gorm"
)

func backfillStorageProfileIDs(
	ctx context.Context,
	db *gorm.DB,
	writes *uow.UnitOfWork,
	registry *storage.Registry,
	revision uint64,
) error {
	var pending int64
	if err := db.WithContext(ctx).Model(&domain.FileObject{}).
		Where("storage_profile_id IS NULL").Count(&pending).Error; err != nil {
		return err
	}
	if pending == 0 {
		return nil
	}

	type match struct {
		profileID string
		count     int
	}
	matches := make(map[string]match)
	for _, profile := range registry.Profiles() {
		candidate := profile.StorageConfig()
		key := candidate.Driver + "\x00" + candidate.Bucket
		entry := matches[key]
		entry.count++
		entry.profileID = profile.ID
		matches[key] = entry
	}

	return writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		var bound int64
		for key, entry := range matches {
			if entry.count != 1 {
				continue
			}
			separator := -1
			for index := range key {
				if key[index] == 0 {
					separator = index
					break
				}
			}
			if separator < 0 {
				continue
			}
			result := tx.Model(&domain.FileObject{}).
				Where("storage_profile_id IS NULL AND provider = ? AND bucket = ?", key[:separator], key[separator+1:]).
				Update("storage_profile_id", entry.profileID)
			if result.Error != nil {
				return frameworkaudit.Event{}, result.Error
			}
			bound += result.RowsAffected
		}
		return frameworkaudit.Event{
			ActorKind:  frameworkaudit.ActorSystem,
			Action:     "storage-profiles:backfill",
			Resource:   "storage-profile",
			ResourceID: "startup",
			Result:     frameworkaudit.ResultSuccess,
			Source:     frameworkaudit.SourceSystem,
			Summary:    "Reconciled historical file storage profiles",
			After: frameworkaudit.SanitizedFields{
				"revision":     revision,
				"boundCount":   bound,
				"unboundCount": pending - bound,
			},
		}, nil
	})
}
