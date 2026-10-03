// Package filereferences shares file-reference persistence and deletion guards
// between the file service, HTTP handlers, and cleanup workers.
package filereferences

import (
	"context"
	"errors"

	frameworkfiles "github.com/xgtian-root/aginex/server/framework/files"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Owner is a permanent serialization point, including for an empty set.
// FileIDs is canonical sorted JSON, updated atomically with Reference rows.
// Its locking read supplies a current set even under MySQL REPEATABLE READ,
// without taking reference locks before file locks.
type Owner struct {
	Resource   string `gorm:"primaryKey;size:120"`
	ResourceID string `gorm:"primaryKey;size:160"`
	FileIDs    string `gorm:"column:file_ids"`
}

func (Owner) TableName() string { return "file_reference_owners" }

type Reference struct {
	Resource   string `gorm:"primaryKey;size:120"`
	ResourceID string `gorm:"primaryKey;size:160"`
	FileID     string `gorm:"primaryKey;size:36"`
}

func (Reference) TableName() string { return "file_references" }

// CheckUnreferenced requires the caller to hold the file object's write lock
// for the whole check and state transition/object deletion. The locking read
// deliberately sees current committed references under MySQL REPEATABLE READ.
func CheckUnreferenced(ctx context.Context, tx *gorm.DB, fileID string) error {
	if ctx == nil || tx == nil || fileID == "" {
		return frameworkfiles.ErrInvalidArgument
	}
	var reference Reference
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("file_id = ?", fileID).Take(&reference).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return frameworkfiles.ErrInUse
}
