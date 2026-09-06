package auth

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	frameworkauthz "github.com/xgtian-root/aginex/server/framework/authz"
	"github.com/xgtian-root/aginex/server/internal/config"
	"github.com/xgtian-root/aginex/server/internal/domain"
	"github.com/xgtian-root/aginex/server/internal/platform/database"
	"github.com/xgtian-root/aginex/server/internal/platform/migrate"
	"github.com/xgtian-root/aginex/server/internal/platform/password"
	"gorm.io/gorm"
)

func TestSessionTokenHashIsKeyedAndStable(t *testing.T) {
	const token = "opaque-session-token"

	first := hashToken(token, "first-session-secret")
	if first != hashToken(token, "first-session-secret") {
		t.Fatal("session token hash is not stable for the same secret")
	}
	if first == hashToken(token, "second-session-secret") {
		t.Fatal("session token hashes must be scoped to the configured secret")
	}
	decoded, err := hex.DecodeString(first)
	if err != nil {
		t.Fatalf("session token hash is not hexadecimal: %v", err)
	}
	if len(decoded) != 32 {
		t.Fatalf("session token hash length = %d, want 32 bytes", len(decoded))
	}
}

func TestPasswordLoginUsesIdentityCredentialAndReturnsCredentialFreeUser(t *testing.T) {
	db := openAuthDatabase(t)
	const (
		email            = "person@example.com"
		passwordValue    = "identity password value"
		legacyOnlyValue  = "legacy users table password"
		sessionSecret    = "test-session-secret-that-is-long-enough"
		sessionIPAddress = "127.0.0.1"
	)
	user, identity := createPasswordIdentity(t, db, email, passwordValue)
	legacyHash, err := password.Hash(legacyOnlyValue)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(
		"UPDATE users SET password_hash = ? WHERE id = ?",
		legacyHash,
		user.ID,
	).Error; err != nil {
		t.Fatal(err)
	}

	service := New(db, config.Session{Secret: sessionSecret, TTL: time.Hour})
	result, err := service.Login(
		"  PERSON@EXAMPLE.COM ",
		passwordValue,
		sessionIPAddress,
		"auth-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.User.ID != user.ID || result.Token == "" || result.SessionID == "" {
		t.Fatalf("login result = %#v", result)
	}
	if result.User.Email != email {
		t.Fatalf("login user email = %q, want %q", result.User.Email, email)
	}

	var session domain.Session
	if err := db.First(&session, "id = ?", result.SessionID).Error; err != nil {
		t.Fatal(err)
	}
	if session.UserID != user.ID || session.IPAddress != sessionIPAddress {
		t.Fatalf("session = %#v", session)
	}
	if session.TokenHash == result.Token {
		t.Fatal("session persisted its plaintext token")
	}
	principal, err := service.Authenticate(result.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range browserSessionPermissions {
		if !principal.Can(code) {
			t.Fatalf("browser principal does not have built-in %q permission", code)
		}
		scope, ok := principal.Scope(code)
		if !ok || scope != frameworkauthz.ScopeOwn {
			t.Fatalf(
				"%s scope = %q, %v; want %q, true",
				code,
				scope,
				ok,
				frameworkauthz.ScopeOwn,
			)
		}
	}

	if _, err := service.Login(email, legacyOnlyValue, "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("legacy-only password error = %v, want ErrInvalidCredentials", err)
	}

	replacementHash, err := password.Hash("different identity password")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.UserIdentity{}).
		Where("id = ?", identity.ID).
		Update("credential_hash", replacementHash).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(email, passwordValue, "", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("stale identity password error = %v, want ErrInvalidCredentials", err)
	}
}

func TestPasswordLoginRejectsInactiveIdentityAndDeletedUser(t *testing.T) {
	db := openAuthDatabase(t)
	user, identity := createPasswordIdentity(
		t,
		db,
		"inactive@example.com",
		"correct inactive password",
	)
	service := New(db, config.Session{
		Secret: "test-session-secret-that-is-long-enough",
		TTL:    time.Hour,
	})

	if err := db.Model(&identity).Update("status", "disabled").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(
		identity.Subject,
		"correct inactive password",
		"",
		"",
	); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled identity error = %v, want ErrInvalidCredentials", err)
	}

	if err := db.Model(&identity).Update("status", domain.IdentityStatusActive).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&user).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Login(
		identity.Subject,
		"correct inactive password",
		"",
		"",
	); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("deleted user error = %v, want ErrInvalidCredentials", err)
	}
}

func TestRevokeAllSessionsTxIsUserScopedAndUsesCallerTransaction(t *testing.T) {
	db := openAuthDatabase(t)
	now := time.Now().UTC()
	firstUser := domain.User{
		ID: uuid.NewString(), Email: "first-sessions@example.com",
		DisplayName: "First user", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	secondUser := domain.User{
		ID: uuid.NewString(), Email: "second-sessions@example.com",
		DisplayName: "Second user", Status: "active", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&[]domain.User{firstUser, secondUser}).Error; err != nil {
		t.Fatal(err)
	}
	sessions := []domain.Session{
		{
			ID: uuid.NewString(), UserID: firstUser.ID, TokenHash: "first-session-a",
			ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
		},
		{
			ID: uuid.NewString(), UserID: firstUser.ID, TokenHash: "first-session-b",
			ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
		},
		{
			ID: uuid.NewString(), UserID: secondUser.ID, TokenHash: "second-session",
			ExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
		},
	}
	if err := db.Create(&sessions).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, config.Session{
		Secret: "test-session-secret-that-is-long-enough",
		TTL:    time.Hour,
	})

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatal(tx.Error)
	}
	revoked, err := service.RevokeAllSessionsTx(tx, firstUser.ID)
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if revoked != 2 {
		_ = tx.Rollback()
		t.Fatalf("transaction revoked = %d, want 2", revoked)
	}
	if err := tx.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	assertSessionCount(t, db, firstUser.ID, 2)
	assertSessionCount(t, db, secondUser.ID, 1)

	revoked, err = service.RevokeAllSessionsTx(db, firstUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked != 2 {
		t.Fatalf("revoked = %d, want 2", revoked)
	}
	assertSessionCount(t, db, firstUser.ID, 0)
	assertSessionCount(t, db, secondUser.ID, 1)

	if _, err := service.RevokeAllSessionsTx(db, " "); !errors.Is(err, ErrUserIDRequired) {
		t.Fatalf("empty user id error = %v, want ErrUserIDRequired", err)
	}
	assertSessionCount(t, db, secondUser.ID, 1)
}

func assertSessionCount(t *testing.T, db *gorm.DB, userID string, want int64) {
	t.Helper()
	var count int64
	if err := db.Model(&domain.Session{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("sessions for %s = %d, want %d", userID, count, want)
	}
}

func openAuthDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "auth.db"),
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
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close auth database: %v", err)
		}
	})
	return db
}

func createPasswordIdentity(
	t *testing.T,
	db *gorm.DB,
	email string,
	passwordValue string,
) (domain.User, domain.UserIdentity) {
	t.Helper()
	now := time.Now().UTC()
	user := domain.User{
		ID:          uuid.NewString(),
		Email:       NormalizePasswordSubject(email),
		DisplayName: "Password user",
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	credentialHash, err := password.Hash(passwordValue)
	if err != nil {
		t.Fatal(err)
	}
	identity := domain.UserIdentity{
		ID:             uuid.NewString(),
		UserID:         user.ID,
		Provider:       domain.IdentityProviderPassword,
		Subject:        NormalizePasswordSubject(email),
		CredentialHash: credentialHash,
		Status:         domain.IdentityStatusActive,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.Create(&identity).Error; err != nil {
		t.Fatal(err)
	}
	return user, identity
}
