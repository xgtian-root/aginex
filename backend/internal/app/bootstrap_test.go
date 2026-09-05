package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/backend/internal/auth"
	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/database"
	"github.com/xgtian-root/aginex/backend/internal/platform/migrate"
	"github.com/xgtian-root/aginex/backend/internal/platform/password"
	"gorm.io/gorm"
)

func TestBootstrapCreatesUserIdentityAndRoleAtomically(t *testing.T) {
	db := openBootstrapDatabase(t)
	cfg := config.Bootstrap{
		AdminEmail:    " ADMIN@Example.com ",
		AdminPassword: "correct bootstrap password",
	}
	if err := Bootstrap(context.Background(), db, cfg); err != nil {
		t.Fatal(err)
	}

	var user domain.User
	if err := db.Preload("Roles").Where("email = ?", "admin@example.com").First(&user).Error; err != nil {
		t.Fatal(err)
	}
	if len(user.Roles) != 1 || user.Roles[0].Name != "Administrator" {
		t.Fatalf("administrator roles = %#v", user.Roles)
	}
	registry, _, err := composeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	var administratorRole domain.Role
	if err := db.Preload("Permissions").
		Where("name = ?", "Administrator").
		First(&administratorRole).Error; err != nil {
		t.Fatal(err)
	}
	if len(administratorRole.Permissions) != len(registry.Permissions()) {
		t.Fatalf(
			"administrator permission count = %d, want %d built-in permissions",
			len(administratorRole.Permissions),
			len(registry.Permissions()),
		)
	}
	var identity domain.UserIdentity
	if err := db.Where(
		"provider = ? AND subject = ?",
		domain.IdentityProviderPassword,
		"admin@example.com",
	).First(&identity).Error; err != nil {
		t.Fatal(err)
	}
	if identity.UserID != user.ID || identity.Status != domain.IdentityStatusActive {
		t.Fatalf("bootstrap identity = %#v", identity)
	}
	if !password.Verify(identity.CredentialHash, cfg.AdminPassword) {
		t.Fatal("bootstrap identity does not verify configured password")
	}
	var legacyHash *string
	if err := db.Raw(
		"SELECT password_hash FROM users WHERE id = ?",
		user.ID,
	).Scan(&legacyHash).Error; err != nil {
		t.Fatal(err)
	}
	if legacyHash != nil {
		t.Fatalf("bootstrap wrote legacy users.password_hash = %q", *legacyHash)
	}

	if err := Bootstrap(context.Background(), db, cfg); err != nil {
		t.Fatal(err)
	}
	assertBootstrapCount(t, db, &domain.User{}, 1)
	assertBootstrapCount(t, db, &domain.UserIdentity{}, 1)
	assertBootstrapCount(t, db, &domain.AuditLog{}, 1)
	var assignmentCount int64
	if err := db.Table("user_roles").
		Where("user_id = ? AND role_id = ?", user.ID, user.Roles[0].ID).
		Count(&assignmentCount).Error; err != nil {
		t.Fatal(err)
	}
	if assignmentCount != 1 {
		t.Fatalf("administrator role assignments = %d, want 1", assignmentCount)
	}
	var audits []domain.AuditLog
	if err := db.Order("created_at ASC").Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	for _, entry := range audits {
		if entry.ActorID == nil || *entry.ActorID != "aginex-bootstrap" {
			t.Fatalf("bootstrap audit actor = %#v", entry.ActorID)
		}
		if entry.ActorKind != "system" ||
			entry.Action != "system:bootstrap" ||
			entry.Resource != "system" ||
			entry.ResourceID != "bootstrap" ||
			entry.Result != "success" ||
			entry.Source != "cli" {
			t.Fatalf("bootstrap audit = %#v", entry)
		}
	}

	service := auth.New(db, config.Session{
		Secret: "test-session-secret-that-is-long-enough",
		TTL:    time.Hour,
	})
	if _, err := service.Login("ADMIN@example.com", cfg.AdminPassword, "", ""); err != nil {
		t.Fatalf("bootstrap administrator login: %v", err)
	}
}

func TestBootstrapRecordsHTTPContextOnlyWhenStateDrifts(t *testing.T) {
	db := openBootstrapDatabase(t)
	cfg := config.Bootstrap{
		AdminEmail:    "setup@example.com",
		AdminPassword: "correct bootstrap password",
	}
	options := BootstrapOptions{
		Source:    "http",
		RequestID: "01JSETUPREQUEST",
		IPAddress: "192.0.2.10",
	}
	if err := BootstrapCompositionWithModulesAndOptions(
		t.Context(),
		db,
		cfg,
		options,
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapCompositionWithModulesAndOptions(
		t.Context(),
		db,
		cfg,
		options,
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatal(err)
	}

	var audits []domain.AuditLog
	if err := db.Find(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("bootstrap audit count = %d, want 1", len(audits))
	}
	if audits[0].Source != "http" ||
		audits[0].RequestID != options.RequestID ||
		audits[0].IPAddress != options.IPAddress {
		t.Fatalf("bootstrap audit context = %#v", audits[0])
	}
}

func TestBootstrapPreservesMigratedPasswordIdentity(t *testing.T) {
	db := openBootstrapDatabase(t)
	const (
		email              = "legacy-admin@example.com"
		migratedPassword   = "migrated administrator password"
		configuredPassword = "replacement bootstrap password"
	)
	now := time.Now().UTC()
	user := domain.User{
		ID:          uuid.NewString(),
		Email:       " Legacy-Admin@Example.com ",
		DisplayName: "Legacy administrator",
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	migratedHash, err := password.Hash(migratedPassword)
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.UserIdentity{
		ID:             user.ID,
		UserID:         user.ID,
		Provider:       domain.IdentityProviderPassword,
		Subject:        email,
		CredentialHash: migratedHash,
		Status:         domain.IdentityStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.Create(&identity).Error; err != nil {
		t.Fatal(err)
	}

	if err := Bootstrap(context.Background(), db, config.Bootstrap{
		AdminEmail:    email,
		AdminPassword: configuredPassword,
	}); err != nil {
		t.Fatal(err)
	}

	var persisted domain.UserIdentity
	if err := db.First(&persisted, "id = ?", identity.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.CredentialHash != migratedHash {
		t.Fatal("bootstrap replaced the migrated password identity")
	}
	var roleCount int64
	if err := db.Table("user_roles").
		Where("user_id = ?", user.ID).
		Count(&roleCount).Error; err != nil {
		t.Fatal(err)
	}
	if roleCount != 1 {
		t.Fatalf("migrated administrator role assignments = %d, want 1", roleCount)
	}

	service := auth.New(db, config.Session{
		Secret: "test-session-secret-that-is-long-enough",
		TTL:    time.Hour,
	})
	if _, err := service.Login(email, migratedPassword, "", ""); err != nil {
		t.Fatalf("migrated password login: %v", err)
	}
	if _, err := service.Login(email, configuredPassword, "", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("replacement bootstrap password error = %v, want invalid credentials", err)
	}
}

func TestHTTPSetupCanReplaceCredentialAfterAnUnsealedAttempt(t *testing.T) {
	db := openBootstrapDatabase(t)
	const (
		email       = "retry-admin@example.com"
		oldPassword = "first setup administrator password"
		newPassword = "replacement setup administrator password"
	)
	if err := Bootstrap(t.Context(), db, config.Bootstrap{
		AdminEmail:    email,
		AdminPassword: oldPassword,
	}); err != nil {
		t.Fatal(err)
	}

	if err := BootstrapWithModulesAndOptions(
		t.Context(),
		db,
		config.Bootstrap{
			AdminEmail:    email,
			AdminPassword: newPassword,
		},
		BootstrapOptions{
			Source:                         "http",
			ReplaceAdministratorCredential: true,
		},
	); err != nil {
		t.Fatal(err)
	}

	var identity domain.UserIdentity
	if err := db.Where(
		"provider = ? AND subject = ?",
		domain.IdentityProviderPassword,
		email,
	).First(&identity).Error; err != nil {
		t.Fatal(err)
	}
	if !password.Verify(identity.CredentialHash, newPassword) {
		t.Fatal("HTTP Setup did not persist the replacement password")
	}
	if password.Verify(identity.CredentialHash, oldPassword) {
		t.Fatal("HTTP Setup retained the previous unsealed password")
	}

	var auditCount int64
	if err := db.Model(&domain.AuditLog{}).
		Where("action = ?", "system:bootstrap").
		Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("bootstrap audit count = %d, want 2", auditCount)
	}
}

func TestBootstrapRollsBackUserAndIdentityWhenRoleAssignmentFails(t *testing.T) {
	db := openBootstrapDatabase(t)
	if err := Bootstrap(context.Background(), db, config.Bootstrap{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TRIGGER reject_bootstrap_user_role
		BEFORE INSERT ON user_roles
		FOR EACH ROW
		BEGIN
			SELECT RAISE(ABORT, 'user role rejected');
		END
	`).Error; err != nil {
		t.Fatal(err)
	}

	const email = "atomic-admin@example.com"
	err := Bootstrap(context.Background(), db, config.Bootstrap{
		AdminEmail:    email,
		AdminPassword: "atomic bootstrap password",
	})
	if err == nil {
		t.Fatal("bootstrap succeeded despite rejected role assignment")
	}

	var userCount int64
	if err := db.Unscoped().Model(&domain.User{}).
		Where("email = ?", email).
		Count(&userCount).Error; err != nil {
		t.Fatal(err)
	}
	if userCount != 0 {
		t.Fatalf("rolled-back bootstrap users = %d, want 0", userCount)
	}
	var identityCount int64
	if err := db.Model(&domain.UserIdentity{}).
		Where("provider = ? AND subject = ?", domain.IdentityProviderPassword, email).
		Count(&identityCount).Error; err != nil {
		t.Fatal(err)
	}
	if identityCount != 0 {
		t.Fatalf("rolled-back bootstrap identities = %d, want 0", identityCount)
	}
}

func TestBootstrapRollsBackAllWritesWhenAuditInsertFails(t *testing.T) {
	db := openBootstrapDatabase(t)
	if err := db.Exec("DROP TABLE audit_logs").Error; err != nil {
		t.Fatal(err)
	}

	err := Bootstrap(context.Background(), db, config.Bootstrap{
		AdminEmail:    "atomic-admin@example.com",
		AdminPassword: "atomic bootstrap password",
	})
	if err == nil {
		t.Fatal("bootstrap succeeded despite missing audit table")
	}

	for table, want := range map[string]int64{
		"permissions":      0,
		"roles":            0,
		"role_permissions": 0,
		"users":            0,
		"user_identities":  0,
		"user_roles":       0,
	} {
		var count int64
		if err := db.Table(table).Count(&count).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d after audit rollback", table, count, want)
		}
	}
}

func TestNewDoesNotBootstrapEvenWhenCredentialsAreConfigured(t *testing.T) {
	db := openBootstrapDatabase(t)
	cfg := config.Config{
		Environment: "test",
		Database: config.Database{
			Driver: "sqlite",
			DSN:    filepath.Join(t.TempDir(), "bootstrap.db"),
		},
		Session: config.Session{
			CookieName: "aginex_session",
			TTL:        time.Hour,
		},
		Idempotency: config.Idempotency{Driver: "disabled"},
		Bootstrap: config.Bootstrap{
			AdminEmail:    "admin@example.com",
			AdminPassword: "must not be used by New",
		},
		Storage: config.Storage{
			Driver:    "local",
			LocalRoot: filepath.Join(t.TempDir(), "uploads"),
		},
	}
	if _, err := New(cfg, db); err != nil {
		t.Fatal(err)
	}

	assertBootstrapCount(t, db, &domain.User{}, 0)
	assertBootstrapCount(t, db, &domain.UserIdentity{}, 0)
	assertBootstrapCount(t, db, &domain.Permission{}, 0)
	assertBootstrapCount(t, db, &domain.Role{}, 0)
	assertBootstrapCount(t, db, &domain.AuditLog{}, 0)
}

func TestBootstrapRequiresCurrentSchemaWithoutApplyingMigrations(t *testing.T) {
	cfg := config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "unmigrated.db"),
	}
	db, err := database.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close unmigrated bootstrap database: %v", err)
		}
	})

	err = Bootstrap(context.Background(), db, config.Bootstrap{})
	if !errors.Is(err, migrate.ErrSchemaNotCurrent) {
		t.Fatalf("Bootstrap error = %v, want ErrSchemaNotCurrent", err)
	}
}

func openBootstrapDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "bootstrap.db"),
	}
	db, err := database.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(sqlDB, cfg.Driver); err != nil {
		t.Fatal(err)
	}
	if err := MigrateModulesUp(
		t.Context(),
		sqlDB,
		cfg.Driver,
		FilesModule(),
		StarterExampleModule(),
	); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close bootstrap database: %v", err)
		}
	})
	return db
}

func assertBootstrapCount(t *testing.T, db *gorm.DB, model any, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(model).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%T count = %d, want %d", model, count, want)
	}
}
