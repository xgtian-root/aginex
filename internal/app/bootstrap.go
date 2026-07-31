package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	frameworkaudit "github.com/xgtian-root/aginex/framework/audit"
	frameworkauthz "github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/framework/module"
	"github.com/xgtian-root/aginex/framework/uow"
	"github.com/xgtian-root/aginex/internal/auth"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/auditlog"
	"github.com/xgtian-root/aginex/internal/platform/migrate"
	"github.com/xgtian-root/aginex/internal/platform/password"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const bootstrapActorID = "aginex-bootstrap"

// Bootstrap synchronizes the built-in permissions and administrator role, and
// optionally creates the configured local administrator. It never migrates the
// database: callers must apply migrations explicitly before invoking it.
func Bootstrap(ctx context.Context, db *gorm.DB, cfg config.Bootstrap) error {
	return BootstrapWithModules(ctx, db, cfg)
}

// BootstrapWithModules synchronizes built-in and application-module
// permissions. POSTA and other derived applications should pass the same
// module set used by NewWithModules so the Administrator role cannot drift
// from the runtime registry.
func BootstrapWithModules(
	ctx context.Context,
	db *gorm.DB,
	cfg config.Bootstrap,
	applicationModules ...module.Module,
) error {
	return bootstrapWithComposition(
		ctx,
		db,
		cfg,
		false,
		applicationModules...,
	)
}

// BootstrapCompositionWithModules synchronizes the core permissions plus
// exactly the supplied module permissions. It does not seed starter/example
// grants unless the caller explicitly registered those modules.
func BootstrapCompositionWithModules(
	ctx context.Context,
	db *gorm.DB,
	cfg config.Bootstrap,
	applicationModules ...module.Module,
) error {
	return bootstrapWithComposition(
		ctx,
		db,
		cfg,
		true,
		applicationModules...,
	)
}

func bootstrapWithComposition(
	ctx context.Context,
	db *gorm.DB,
	cfg config.Bootstrap,
	exactComposition bool,
	applicationModules ...module.Module,
) error {
	if ctx == nil {
		return fmt.Errorf("bootstrap context is required")
	}
	if db == nil {
		return fmt.Errorf("bootstrap database is required")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("access bootstrap database: %w", err)
	}
	if err := migrate.EnsureCurrent(ctx, sqlDB, db.Dialector.Name()); err != nil {
		return fmt.Errorf("check database schema before bootstrap: %w", err)
	}
	var registry *module.Registry
	if exactComposition {
		registry, _, err = composeDefinitionRegistry(
			applicationModules...,
		)
	} else {
		registry, _, err = composeRegistry(applicationModules...)
	}
	if err != nil {
		return err
	}
	if err := ensureModuleMigrationsCurrent(
		ctx,
		sqlDB,
		db.Dialector.Name(),
		registry,
	); err != nil {
		return err
	}
	subject := auth.NormalizePasswordSubject(cfg.AdminEmail)
	hasAdministrator := subject != "" && cfg.AdminPassword != ""
	if (subject == "") != (cfg.AdminPassword == "") {
		return fmt.Errorf(
			"bootstrap administrator email and password must be configured together",
		)
	}
	cfg.AdminEmail = subject

	writes, err := uow.New(db, auditlog.Recorder{})
	if err != nil {
		return fmt.Errorf("configure bootstrap unit of work: %w", err)
	}
	return writes.Run(ctx, func(tx *gorm.DB) (frameworkaudit.Event, error) {
		if err := bootstrapTx(tx, cfg, registry.Permissions()); err != nil {
			return frameworkaudit.Event{}, err
		}
		actorID := bootstrapActorID
		return frameworkaudit.Event{
			ActorID:    &actorID,
			ActorKind:  frameworkaudit.ActorSystem,
			Action:     "system:bootstrap",
			Resource:   "system",
			ResourceID: "bootstrap",
			Result:     frameworkaudit.ResultSuccess,
			Source:     frameworkaudit.SourceCLI,
			Summary:    "Synchronized registered permissions and administrator access",
			After: map[string]any{
				"administratorConfigured": hasAdministrator,
				"permissionCount":         len(registry.Permissions()),
			},
		}, nil
	})
}

func bootstrapTx(
	tx *gorm.DB,
	cfg config.Bootstrap,
	definitions []module.PermissionDefinition,
) error {
	now := time.Now().UTC()
	permissions := make([]domain.Permission, 0, len(definitions))
	permissionCodes := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		permissions = append(permissions, domain.Permission{
			ID: uuid.NewString(), Code: definition.Code, Description: definition.Description, CreatedAt: now,
		})
		permissionCodes = append(permissionCodes, definition.Code)
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "code"}},
		DoUpdates: clause.AssignmentColumns([]string{"description"}),
	}).Create(&permissions).Error; err != nil {
		return fmt.Errorf("seed permissions: %w", err)
	}

	role := domain.Role{
		ID: uuid.NewString(), Name: "Administrator", Description: "Full framework access", CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"description", "updated_at"}),
	}).Create(&role).Error; err != nil {
		return fmt.Errorf("seed administrator role: %w", err)
	}
	role = domain.Role{}
	if err := tx.Where("name = ?", "Administrator").First(&role).Error; err != nil {
		return err
	}
	permissions = nil
	if err := tx.Where("code IN ?", permissionCodes).Find(&permissions).Error; err != nil {
		return err
	}
	if err := tx.Model(&role).Association("Permissions").Replace(permissions); err != nil {
		return fmt.Errorf("assign administrator permissions: %w", err)
	}
	if err := tx.Table("role_permissions").
		Where("role_id = ?", role.ID).
		Update("scope", frameworkauthz.ScopeAll).Error; err != nil {
		return fmt.Errorf("scope administrator permissions: %w", err)
	}

	if cfg.AdminEmail == "" {
		return nil
	}
	subject := cfg.AdminEmail
	var identity domain.UserIdentity
	identityQuery := tx.Where(
		"provider = ? AND subject = ?",
		domain.IdentityProviderPassword,
		subject,
	).Limit(1).Find(&identity)
	if identityQuery.Error != nil {
		return fmt.Errorf("find bootstrap password identity: %w", identityQuery.Error)
	}
	identityFound := identityQuery.RowsAffected == 1

	var user domain.User
	var userQuery *gorm.DB
	if identityFound {
		userQuery = tx.Where("id = ?", identity.UserID).Limit(1).Find(&user)
	} else {
		userQuery = tx.Where("LOWER(TRIM(email)) = ?", subject).Limit(1).Find(&user)
	}
	if userQuery.Error != nil {
		return fmt.Errorf("find bootstrap administrator: %w", userQuery.Error)
	}
	if userQuery.RowsAffected == 0 {
		if identityFound {
			return fmt.Errorf(
				"bootstrap password identity %q references a missing or deleted user",
				subject,
			)
		}
		user = domain.User{
			ID:          uuid.NewString(),
			Email:       subject,
			DisplayName: "Administrator",
			Status:      "active",
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Create(&user).Error; err != nil {
			return fmt.Errorf("create bootstrap administrator: %w", err)
		}
	}

	if identityFound {
		if identity.UserID != user.ID {
			return fmt.Errorf(
				"bootstrap password identity %q belongs to a different user",
				subject,
			)
		}
	} else {
		passwordHash, hashErr := password.Hash(cfg.AdminPassword)
		if hashErr != nil {
			return fmt.Errorf("hash bootstrap password: %w", hashErr)
		}
		identity = domain.UserIdentity{
			ID:             uuid.NewString(),
			UserID:         user.ID,
			Provider:       domain.IdentityProviderPassword,
			Subject:        subject,
			CredentialHash: passwordHash,
			Status:         domain.IdentityStatusActive,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&identity).Error; err != nil {
			return fmt.Errorf("create bootstrap password identity: %w", err)
		}
	}

	if err := tx.Model(&user).Association("Roles").Append(&role); err != nil {
		return fmt.Errorf("assign administrator role: %w", err)
	}
	return nil
}
