package files

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/google/uuid"
	frameworkfiles "github.com/xgtian-root/aginex/server/framework/files"
	"github.com/xgtian-root/aginex/server/framework/uow"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/filereferences"
	"github.com/xgtian-root/aginex/server/internal/platform/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type service struct {
	db       *gorm.DB
	registry storageResolver
}

type storageResolver interface {
	Resolve(string) (storage.Storage, error)
	Profile(string) (config.StorageProfile, bool)
}

// New shares the process's immutable storage registry; it never substitutes
// the active profile for an unavailable original profile.
func New(db *gorm.DB, registry *storage.Registry) (frameworkfiles.Service, error) {
	if db == nil || registry == nil {
		return nil, frameworkfiles.ErrInvalidArgument
	}
	return &service{db: db, registry: registry}, nil
}

func (service *service) GetReady(ctx context.Context, id string) (frameworkfiles.Info, error) {
	file, err := service.ready(ctx, id)
	if err != nil {
		return frameworkfiles.Info{}, err
	}
	return info(file), nil
}

func (service *service) Open(ctx context.Context, id string) (frameworkfiles.Info, io.ReadCloser, error) {
	file, err := service.ready(ctx, id)
	if err != nil {
		return frameworkfiles.Info{}, nil, err
	}
	if file.StorageProfileID == nil || *file.StorageProfileID == "" {
		return frameworkfiles.Info{}, nil, frameworkfiles.ErrStorageUnavailable
	}
	profile, ok := service.registry.Profile(*file.StorageProfileID)
	if !ok || profile.StorageConfig().Driver != file.Provider || profile.StorageConfig().Bucket != file.Bucket {
		return frameworkfiles.Info{}, nil, frameworkfiles.ErrStorageUnavailable
	}
	store, err := service.registry.Resolve(*file.StorageProfileID)
	if err != nil {
		return frameworkfiles.Info{}, nil, frameworkfiles.ErrStorageUnavailable
	}
	reader, err := store.Open(ctx, file.ObjectKey)
	if err != nil {
		if ctx.Err() != nil {
			return frameworkfiles.Info{}, nil, ctx.Err()
		}
		// Provider errors can contain bucket names, paths, and credentials.
		// Only the stable public error crosses the application boundary.
		return frameworkfiles.Info{}, nil, frameworkfiles.ErrStorageUnavailable
	}
	return info(file), reader, nil
}

func (service *service) ready(ctx context.Context, id string) (domain.FileObject, error) {
	var file domain.FileObject
	if ctx == nil || !validFileID(id) {
		return file, frameworkfiles.ErrInvalidArgument
	}
	err := service.db.WithContext(ctx).First(&file, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return file, frameworkfiles.ErrNotFound
	}
	if err != nil {
		return file, err
	}
	if file.Status != "ready" || file.DeletedAt != nil {
		return file, frameworkfiles.ErrNotReady
	}
	return file, nil
}

func (service *service) Bind(tx *gorm.DB) (frameworkfiles.References, error) {
	if err := uow.RequireTransaction(service.db, tx); err != nil {
		return nil, errors.Join(frameworkfiles.ErrInvalidTransaction, err)
	}
	return &references{db: service.db, tx: tx}, nil
}

type references struct {
	db *gorm.DB
	tx *gorm.DB
}

var resourcePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func (references *references) Replace(ctx context.Context, owner frameworkfiles.ReferenceOwner, ids []string) ([]frameworkfiles.Info, error) {
	if ctx == nil || !validOwner(owner) {
		return nil, frameworkfiles.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	next, err := normalizeIDs(ids)
	if err != nil {
		return nil, err
	}
	if err := uow.RequireTransaction(references.db, references.tx); err != nil {
		return nil, errors.Join(frameworkfiles.ErrInvalidTransaction, err)
	}
	var result []frameworkfiles.Info
	// A savepoint restores the replacement after a handled validation or child
	// cancellation error. Database failures must still propagate to Writes.Run;
	// a broken transaction cannot be assumed to recover at a savepoint.
	// GORM rolls back using the savepoint's context. Keep that cleanup alive
	// when Replace's child context (including a context passed to Bind) is
	// canceled. The underlying SQL transaction still belongs to Writes.Run's
	// context and cannot outlive it; only the replacement SQL uses this child.
	err = references.tx.WithContext(context.WithoutCancel(ctx)).Transaction(func(tx *gorm.DB) error {
		var replaceErr error
		result, replaceErr = replace(tx.WithContext(ctx), owner, next)
		return replaceErr
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func replace(tx *gorm.DB, owner frameworkfiles.ReferenceOwner, next []string) ([]frameworkfiles.Info, error) {
	lockedOwner := filereferences.Owner{Resource: owner.Resource, ResourceID: owner.ResourceID, FileIDs: "[]"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&lockedOwner).Error; err != nil {
		return nil, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("resource = ? AND resource_id = ?", owner.Resource, owner.ResourceID).
		First(&lockedOwner).Error; err != nil {
		return nil, err
	}
	var previous []string
	if err := json.Unmarshal([]byte(lockedOwner.FileIDs), &previous); err != nil {
		return nil, frameworkfiles.ErrInvalidState
	}
	canonical, err := normalizeIDs(previous)
	if err != nil || !slices.Equal(previous, canonical) || canonicalJSON(canonical) != lockedOwner.FileIDs {
		return nil, frameworkfiles.ErrInvalidState
	}
	all := append(slices.Clone(previous), next...)
	slices.Sort(all)
	all = slices.Compact(all)
	lockedFiles := make(map[string]domain.FileObject, len(all))
	for _, id := range all {
		var file domain.FileObject
		// Individual locking reads make the acquisition order explicit; an
		// ORDER BY on one multi-row query need not dictate its query plan.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&file, "id = ?", id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, frameworkfiles.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		lockedFiles[id] = file
	}
	result := make([]frameworkfiles.Info, 0, len(next))
	for _, id := range next {
		file := lockedFiles[id]
		if file.Status != "ready" || file.DeletedAt != nil {
			return nil, frameworkfiles.ErrNotReady
		}
		result = append(result, info(file))
	}
	// This current read comes only after all file locks. It detects state
	// corruption without acquiring reference locks before a deletion's file
	// lock, and never relies on an earlier MySQL repeatable-read snapshot.
	var rows []filereferences.Reference
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("resource = ? AND resource_id = ?", owner.Resource, owner.ResourceID).
		Order("file_id").Find(&rows).Error; err != nil {
		return nil, err
	}
	actual := make([]string, 0, len(rows))
	for _, row := range rows {
		actual = append(actual, row.FileID)
	}
	if !slices.Equal(actual, previous) {
		return nil, frameworkfiles.ErrInvalidState
	}
	for _, id := range previous {
		if _, present := slices.BinarySearch(next, id); present {
			continue
		}
		if err := tx.Where("resource = ? AND resource_id = ? AND file_id = ?", owner.Resource, owner.ResourceID, id).
			Delete(&filereferences.Reference{}).Error; err != nil {
			return nil, err
		}
	}
	for _, id := range next {
		if _, present := slices.BinarySearch(previous, id); present {
			continue
		}
		if err := tx.Create(&filereferences.Reference{Resource: owner.Resource, ResourceID: owner.ResourceID, FileID: id}).Error; err != nil {
			return nil, err
		}
	}
	if !slices.Equal(previous, next) {
		if err := tx.Model(&lockedOwner).Update("file_ids", canonicalJSON(next)).Error; err != nil {
			return nil, err
		}
	}
	return result, nil
}

func normalizeIDs(ids []string) ([]string, error) {
	if len(ids) > frameworkfiles.MaxReferences {
		return nil, fmt.Errorf("%w: at most %d file IDs are allowed", frameworkfiles.ErrInvalidArgument, frameworkfiles.MaxReferences)
	}
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if !validFileID(id) {
			return nil, frameworkfiles.ErrInvalidArgument
		}
		result = append(result, id)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

func canonicalJSON(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	encoded, _ := json.Marshal(ids)
	return string(encoded)
}

func validFileID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func validOwner(owner frameworkfiles.ReferenceOwner) bool {
	return len(owner.Resource) <= 120 && resourcePattern.MatchString(owner.Resource) &&
		owner.ResourceID != "" && len(owner.ResourceID) <= 160 &&
		strings.TrimSpace(owner.ResourceID) == owner.ResourceID &&
		!strings.ContainsFunc(owner.ResourceID, unicode.IsControl)
}

func info(file domain.FileObject) frameworkfiles.Info {
	return frameworkfiles.Info{
		ID: file.ID, OriginalName: file.OriginalName, ContentType: file.ContentType,
		Size: file.Size, Width: file.Width, Height: file.Height, SHA256: file.SHA256,
		OwnerID: file.OwnerID, Visibility: file.Visibility,
	}
}
