package app

import (
	"context"
	"fmt"

	"github.com/xgtian-root/aginex/internal/auth"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/password"
	"gorm.io/gorm"
)

// HasActiveAdministrator reports whether the application has at least one
// active, non-deleted user with an active password identity and the built-in
// Administrator role. Startup uses this after bootstrap so a configured
// installation can never become ready without a usable first sign-in path.
func HasActiveAdministrator(ctx context.Context, db *gorm.DB) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("administrator check context is required")
	}
	if db == nil {
		return false, fmt.Errorf("administrator check database is required")
	}

	hashes, err := activeAdministratorCredentialHashes(ctx, db, "")
	if err != nil {
		return false, err
	}
	for _, hash := range hashes {
		if password.ValidHash(hash) {
			return true, nil
		}
	}
	return false, nil
}

// HasActiveAdministratorCredentials verifies that the submitted password
// belongs to the exact active Administrator identity established by HTTP
// Setup. This prevents a pre-existing or partially initialized database from
// activating with credentials different from the ones the operator supplied.
func HasActiveAdministratorCredentials(
	ctx context.Context,
	db *gorm.DB,
	email string,
	credential string,
) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("administrator check context is required")
	}
	if db == nil {
		return false, fmt.Errorf("administrator check database is required")
	}
	subject := auth.NormalizePasswordSubject(email)
	if subject == "" || credential == "" {
		return false, nil
	}
	hashes, err := activeAdministratorCredentialHashes(ctx, db, subject)
	if err != nil {
		return false, err
	}
	for _, hash := range hashes {
		if password.Verify(hash, credential) {
			return true, nil
		}
	}
	return false, nil
}

func activeAdministratorCredentialHashes(
	ctx context.Context,
	db *gorm.DB,
	subject string,
) ([]string, error) {
	query := db.WithContext(ctx).
		Table("users AS u").
		Joins("JOIN user_identities AS ui ON ui.user_id = u.id").
		Joins("JOIN user_roles AS ur ON ur.user_id = u.id").
		Joins("JOIN roles AS r ON r.id = ur.role_id").
		Where("u.deleted_at IS NULL").
		Where("u.status = ?", "active").
		Where("ui.provider = ?", domain.IdentityProviderPassword).
		Where("ui.status = ?", domain.IdentityStatusActive).
		Where("r.name = ?", "Administrator")
	if subject != "" {
		query = query.Where("ui.subject = ?", subject)
	}
	var hashes []string
	if err := query.Distinct().Pluck("ui.credential_hash", &hashes).Error; err != nil {
		return nil, fmt.Errorf("check active administrator: %w", err)
	}
	return hashes, nil
}
