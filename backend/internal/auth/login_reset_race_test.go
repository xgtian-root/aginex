package auth

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/xgtian-root/aginex/backend/internal/config"
	"github.com/xgtian-root/aginex/backend/internal/domain"
	"github.com/xgtian-root/aginex/backend/internal/platform/password"
	"gorm.io/gorm"
)

type loginResetRaceMarkerKey struct{}

func TestPasswordResetLinearizesWithOldPasswordSessionCreation(t *testing.T) {
	db := openAuthDatabase(t)
	const (
		email       = "login-reset-race@example.com"
		oldPassword = "old login reset race password value"
		newPassword = "new login reset race password value"
	)
	user, _ := createPasswordIdentity(t, db, email, oldPassword)
	service := New(db, testSessionConfig())
	newHash, err := password.Hash(newPassword)
	if err != nil {
		t.Fatal(err)
	}

	loginReady := make(chan struct{})
	allowLogin := make(chan struct{})
	resetRead := make(chan struct{})
	var loginSignal sync.Once
	if err := db.Callback().Create().Before("gorm:create").Register(
		"test:block_old_password_session_create",
		func(tx *gorm.DB) {
			if tx.Statement.Context.Value(loginResetRaceMarkerKey{}) != "login" ||
				tx.Statement.Table != "sessions" {
				return
			}
			loginSignal.Do(func() {
				close(loginReady)
				<-allowLogin
			})
		},
	); err != nil {
		t.Fatal(err)
	}
	var resetSignal sync.Once
	if err := db.Callback().Query().After("gorm:query").Register(
		"test:observe_reset_identity_read",
		func(tx *gorm.DB) {
			if tx.Statement.Context.Value(loginResetRaceMarkerKey{}) != "reset" ||
				tx.Statement.Table != "user_identities" {
				return
			}
			resetSignal.Do(func() { close(resetRead) })
		},
	); err != nil {
		t.Fatal(err)
	}

	type loginOutcome struct {
		result LoginResult
		err    error
	}
	loginDone := make(chan loginOutcome, 1)
	loginContext := context.WithValue(
		context.Background(),
		loginResetRaceMarkerKey{},
		"login",
	)
	go func() {
		result, loginErr := service.LoginContext(
			loginContext,
			email,
			oldPassword,
			"",
			"race-test",
		)
		loginDone <- loginOutcome{result: result, err: loginErr}
	}()
	waitForAuthRaceSignal(t, loginReady, "old-password login to reach session creation")

	resetCredential := func(ctx context.Context) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			identities, lockErr := LockPasswordIdentitiesTx(tx, user.ID)
			if lockErr != nil {
				return lockErr
			}
			if len(identities) != 1 {
				return errors.New("password identity count changed")
			}
			if updateErr := tx.Model(&domain.UserIdentity{}).
				Where("id = ?", identities[0].ID).
				Updates(map[string]any{
					"credential_hash": newHash,
					"updated_at":      time.Now().UTC(),
				}).Error; updateErr != nil {
				return updateErr
			}
			_, revokeErr := service.RevokeAllSessionsTx(tx, user.ID)
			return revokeErr
		})
	}
	resetDone := make(chan error, 1)
	resetContext := context.WithValue(
		context.Background(),
		loginResetRaceMarkerKey{},
		"reset",
	)
	go func() { resetDone <- resetCredential(resetContext) }()
	// SQLite uses immediate write transactions in production so the reset may
	// wait at Begin until the login transaction commits. Other databases can
	// reach the locked identity read first. Exercise either valid
	// linearization, then retain the same final session-revocation assertions.
	select {
	case <-resetRead:
	case <-time.After(100 * time.Millisecond):
	}
	close(allowLogin)

	var login loginOutcome
	select {
	case login = <-loginDone:
	case <-time.After(5 * time.Second):
		t.Fatal("old-password login did not finish")
	}
	if login.err != nil {
		t.Fatalf("old-password login that held the identity lock failed: %v", login.err)
	}

	var resetErr error
	select {
	case resetErr = <-resetDone:
	case <-time.After(5 * time.Second):
		t.Fatal("interleaved password reset did not finish")
	}
	// SQLite can fail the read-snapshot upgrade closed instead of waiting for
	// the first writer. Retrying after login commits exercises the same final
	// ordering as PostgreSQL/MySQL waiting on FOR UPDATE.
	if resetErr != nil {
		if retryErr := resetCredential(context.Background()); retryErr != nil {
			t.Fatalf("retry password reset after serialization failure: %v", retryErr)
		}
	}
	waitForAuthRaceSignal(t, resetRead, "password reset to read the locked identity")

	if _, authenticateErr := service.Authenticate(login.result.Token); authenticateErr == nil {
		t.Fatal("old-password session remained authenticatable after password reset")
	}
	assertSessionCount(t, db, user.ID, 0)
	if _, loginErr := service.Login(email, oldPassword, "", ""); !errors.Is(loginErr, ErrInvalidCredentials) {
		t.Fatalf("old password error = %v, want ErrInvalidCredentials", loginErr)
	}
	newLogin, loginErr := service.Login(email, newPassword, "", "")
	if loginErr != nil {
		t.Fatalf("new password login: %v", loginErr)
	}
	if _, authenticateErr := service.Authenticate(newLogin.Token); authenticateErr != nil {
		t.Fatalf("new password session authentication: %v", authenticateErr)
	}
}

func waitForAuthRaceSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for " + description)
	}
}

func testSessionConfig() config.Session {
	return config.Session{
		Secret: "test-session-secret-that-is-long-enough",
		TTL:    time.Hour,
	}
}
