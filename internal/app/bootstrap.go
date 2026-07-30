package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian/aginex/internal/config"
	"github.com/xgtian/aginex/internal/domain"
	"github.com/xgtian/aginex/internal/platform/password"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var builtInPermissions = map[string]string{
	"dashboard:read":  "View the dashboard",
	"products:create": "Create products",
	"products:read":   "View products",
	"products:update": "Update products",
	"products:delete": "Delete products",
	"users:read":      "View users",
	"roles:read":      "View roles and permissions",
	"audit:read":      "View audit history",
	"files:create":    "Upload files",
	"files:read":      "View files",
	"files:delete":    "Delete files",
}

func bootstrap(db *gorm.DB, cfg config.Bootstrap) error {
	now := time.Now().UTC()
	permissions := make([]domain.Permission, 0, len(builtInPermissions))
	for code, description := range builtInPermissions {
		permissions = append(permissions, domain.Permission{
			ID: uuid.NewString(), Code: code, Description: description, CreatedAt: now,
		})
	}
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "code"}}, DoNothing: true}).Create(&permissions).Error; err != nil {
		return fmt.Errorf("seed permissions: %w", err)
	}

	role := domain.Role{
		ID: uuid.NewString(), Name: "Administrator", Description: "Full framework access", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoNothing: true}).Create(&role).Error; err != nil {
		return fmt.Errorf("seed administrator role: %w", err)
	}
	role = domain.Role{}
	if err := db.Where("name = ?", "Administrator").First(&role).Error; err != nil {
		return err
	}
	permissions = nil
	if err := db.Find(&permissions).Error; err != nil {
		return err
	}
	if err := db.Model(&role).Association("Permissions").Replace(permissions); err != nil {
		return fmt.Errorf("assign administrator permissions: %w", err)
	}

	if cfg.AdminEmail == "" || cfg.AdminPassword == "" {
		return nil
	}
	var count int64
	if err := db.Model(&domain.User{}).Where("email = ?", strings.ToLower(cfg.AdminEmail)).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	passwordHash, err := password.Hash(cfg.AdminPassword)
	if err != nil {
		return fmt.Errorf("hash bootstrap password: %w", err)
	}
	user := domain.User{
		ID:           uuid.NewString(),
		Email:        strings.ToLower(cfg.AdminEmail),
		DisplayName:  "Administrator",
		PasswordHash: passwordHash,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := db.Create(&user).Error; err != nil {
		return fmt.Errorf("create bootstrap administrator: %w", err)
	}
	if err := db.Model(&user).Association("Roles").Append(&role); err != nil {
		return fmt.Errorf("assign administrator role: %w", err)
	}
	return nil
}
