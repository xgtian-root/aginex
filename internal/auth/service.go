package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	frameworkauthz "github.com/xgtian-root/aginex/framework/authz"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/password"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserIDRequired     = errors.New("user id is required")
)

var browserSessionPermissions = [...]string{
	"sessions:read",
	"sessions:delete",
}

type Principal struct {
	User        domain.User
	Permissions map[string]struct{}
	GrantScopes map[string]frameworkauthz.GrantScope
	SessionID   string
}

type LoginResult struct {
	Token     string
	User      domain.User
	SessionID string
}

func (p Principal) Can(permission string) bool {
	if _, ok := p.GrantScopes[permission]; ok {
		return true
	}
	_, legacy := p.Permissions[permission]
	return legacy
}

func (p Principal) Scope(permission string) (frameworkauthz.GrantScope, bool) {
	scope, ok := p.GrantScopes[permission]
	return scope, ok
}

func (p Principal) Actor() frameworkauthz.Actor {
	codes := make([]string, 0, len(p.GrantScopes))
	for code := range p.GrantScopes {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	grants := make([]frameworkauthz.Grant, 0, len(codes))
	for _, code := range codes {
		grants = append(grants, frameworkauthz.Grant{
			Permission: code,
			Scope:      p.GrantScopes[code],
		})
	}
	return frameworkauthz.NewUserActor(p.User.ID, grants...)
}

type Service struct {
	db  *gorm.DB
	cfg config.Session
}

func New(db *gorm.DB, cfg config.Session) *Service {
	return &Service{db: db, cfg: cfg}
}

func NormalizePasswordSubject(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func (s *Service) Login(email, value, ipAddress, userAgent string) (LoginResult, error) {
	return s.LoginContext(
		context.Background(),
		email,
		value,
		ipAddress,
		userAgent,
	)
}

// LoginContext authenticates a password identity and creates a revocable
// browser session using the caller's trace and cancellation context.
func (s *Service) LoginContext(
	ctx context.Context,
	email,
	value,
	ipAddress,
	userAgent string,
) (LoginResult, error) {
	if ctx == nil {
		return LoginResult{}, errors.New("login context is required")
	}
	var result LoginResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var loginErr error
		result, loginErr = s.LoginTx(
			tx,
			email,
			value,
			ipAddress,
			userAgent,
		)
		return loginErr
	})
	return result, err
}

func (s *Service) LoginTx(
	tx *gorm.DB,
	email,
	value,
	ipAddress,
	userAgent string,
) (LoginResult, error) {
	var identity domain.UserIdentity
	identityQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
		"provider = ? AND subject = ? AND status = ?",
		domain.IdentityProviderPassword,
		NormalizePasswordSubject(email),
		domain.IdentityStatusActive,
	).Limit(1).Find(&identity)
	if identityQuery.Error != nil {
		return LoginResult{}, identityQuery.Error
	}
	if identityQuery.RowsAffected == 0 {
		password.VerifyDummy(value)
		return LoginResult{}, ErrInvalidCredentials
	}
	if err := lockPasswordIdentityRowTx(tx, identity.ID); err != nil {
		return LoginResult{}, err
	}
	var user domain.User
	userQuery := tx.Where(
		"id = ? AND status = ?",
		identity.UserID,
		"active",
	).Limit(1).Find(&user)
	if userQuery.Error != nil {
		return LoginResult{}, userQuery.Error
	}
	if userQuery.RowsAffected == 0 {
		password.VerifyDummy(value)
		return LoginResult{}, ErrInvalidCredentials
	}
	if !password.Verify(identity.CredentialHash, value) {
		return LoginResult{}, ErrInvalidCredentials
	}

	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return LoginResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	now := time.Now().UTC()
	session := domain.Session{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		TokenHash:  hashToken(token, s.cfg.Secret),
		ExpiresAt:  now.Add(s.cfg.TTL),
		CreatedAt:  now,
		LastSeenAt: now,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
	}
	if err := tx.Create(&session).Error; err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, User: user, SessionID: session.ID}, nil
}

func lockPasswordIdentityRowTx(tx *gorm.DB, identityID string) error {
	// SQLite omits FOR UPDATE. The no-op write acquires its single-writer lock
	// before password verification and session insertion; PostgreSQL and MySQL
	// retain the row lock already acquired by the current read. A concurrent
	// credential reset therefore either deletes the newly committed session or
	// commits first and makes login verify the replacement hash.
	return tx.Model(&domain.UserIdentity{}).
		Where("id = ?", identityID).
		UpdateColumn("updated_at", gorm.Expr("updated_at")).Error
}

// LockPasswordIdentitiesTx returns every local password identity for one user
// while holding locks that serialize credential changes and session creation.
// Callers must keep the supplied transaction open through credential mutation
// and session revocation.
func LockPasswordIdentitiesTx(
	tx *gorm.DB,
	userID string,
) ([]domain.UserIdentity, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, ErrUserIDRequired
	}
	var identities []domain.UserIdentity
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND provider = ?", userID, domain.IdentityProviderPassword).
		Order("id ASC").
		Find(&identities).Error; err != nil {
		return nil, err
	}
	for _, identity := range identities {
		if err := lockPasswordIdentityRowTx(tx, identity.ID); err != nil {
			return nil, err
		}
	}
	return identities, nil
}

func (s *Service) Authenticate(token string) (Principal, error) {
	return s.AuthenticateContext(context.Background(), token)
}

// AuthenticateContext resolves one browser session and its grants using the
// request context so database spans remain children of the HTTP trace.
func (s *Service) AuthenticateContext(
	ctx context.Context,
	token string,
) (Principal, error) {
	if ctx == nil {
		return Principal{}, errors.New("authentication context is required")
	}
	if token == "" {
		return Principal{}, gorm.ErrRecordNotFound
	}
	db := s.db.WithContext(ctx)
	var session domain.Session
	if err := db.Where("token_hash = ? AND expires_at > ?", hashToken(token, s.cfg.Secret), time.Now().UTC()).First(&session).Error; err != nil {
		return Principal{}, err
	}
	var user domain.User
	if err := db.First(&user, "id = ? AND status = ?", session.UserID, "active").Error; err != nil {
		return Principal{}, err
	}
	type permissionGrant struct {
		Code  string
		Scope string
	}
	var permissionGrants []permissionGrant
	err := db.
		Table("permissions").
		Select("permissions.code, role_permissions.scope").
		Joins("JOIN role_permissions ON role_permissions.permission_id = permissions.id").
		Joins("JOIN user_roles ON user_roles.role_id = role_permissions.role_id").
		Where("user_roles.user_id = ?", user.ID).
		Scan(&permissionGrants).Error
	if err != nil {
		return Principal{}, err
	}
	codes := make(map[string]struct{}, len(permissionGrants))
	scopes := make(map[string]frameworkauthz.GrantScope, len(permissionGrants))
	for _, grant := range permissionGrants {
		scope := frameworkauthz.GrantScope(grant.Scope)
		if scope != frameworkauthz.ScopeOwn && scope != frameworkauthz.ScopeAll {
			return Principal{}, fmt.Errorf("permission %s has invalid scope %q", grant.Code, grant.Scope)
		}
		codes[grant.Code] = struct{}{}
		if existing, ok := scopes[grant.Code]; ok && existing == frameworkauthz.ScopeAll {
			continue
		}
		scopes[grant.Code] = scope
	}
	// Every valid browser principal owns its current session and may inspect or
	// revoke only its own session aggregate without requiring an application
	// role. An explicit all-scope role grant is preserved.
	for _, code := range browserSessionPermissions {
		codes[code] = struct{}{}
		if _, exists := scopes[code]; !exists {
			scopes[code] = frameworkauthz.ScopeOwn
		}
	}
	return Principal{
		User: user, Permissions: codes, GrantScopes: scopes, SessionID: session.ID,
	}, nil
}

func (s *Service) Logout(token string) error {
	return s.LogoutContext(context.Background(), token)
}

// LogoutContext revokes one browser session using the caller's context.
func (s *Service) LogoutContext(ctx context.Context, token string) error {
	if ctx == nil {
		return errors.New("logout context is required")
	}
	return s.LogoutTx(s.db.WithContext(ctx), token)
}

func (s *Service) LogoutTx(tx *gorm.DB, token string) error {
	if token == "" {
		return nil
	}
	return tx.Where("token_hash = ?", hashToken(token, s.cfg.Secret)).Delete(&domain.Session{}).Error
}

// RevokeAllSessionsTx revokes every browser session owned by exactly one user.
// Callers that also persist an audit event must pass the same transaction.
func (s *Service) RevokeAllSessionsTx(tx *gorm.DB, userID string) (int64, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return 0, ErrUserIDRequired
	}
	result := tx.Where("user_id = ?", userID).Delete(&domain.Session{})
	return result.RowsAffected, result.Error
}

func hashToken(token, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}
