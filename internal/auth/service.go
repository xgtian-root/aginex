package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xgtian-root/aginex/internal/config"
	"github.com/xgtian-root/aginex/internal/domain"
	"github.com/xgtian-root/aginex/internal/platform/password"
	"gorm.io/gorm"
)

var ErrInvalidCredentials = errors.New("invalid email or password")

type Principal struct {
	User        domain.User
	Permissions map[string]struct{}
	SessionID   string
}

func (p Principal) Can(permission string) bool {
	_, ok := p.Permissions[permission]
	return ok
}

type Service struct {
	db  *gorm.DB
	cfg config.Session
}

func New(db *gorm.DB, cfg config.Session) *Service {
	return &Service{db: db, cfg: cfg}
}

func (s *Service) Login(email, value, ipAddress, userAgent string) (string, domain.User, error) {
	var user domain.User
	if err := s.db.Where("email = ? AND status = ?", strings.ToLower(strings.TrimSpace(email)), "active").First(&user).Error; err != nil {
		return "", domain.User{}, ErrInvalidCredentials
	}
	if !password.Verify(user.PasswordHash, value) {
		return "", domain.User{}, ErrInvalidCredentials
	}

	rawToken := make([]byte, 32)
	if _, err := rand.Read(rawToken); err != nil {
		return "", domain.User{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	now := time.Now().UTC()
	session := domain.Session{
		ID:         uuid.NewString(),
		UserID:     user.ID,
		TokenHash:  hashToken(token),
		ExpiresAt:  now.Add(s.cfg.TTL),
		CreatedAt:  now,
		LastSeenAt: now,
		IPAddress:  ipAddress,
		UserAgent:  userAgent,
	}
	if err := s.db.Create(&session).Error; err != nil {
		return "", domain.User{}, err
	}
	return token, user, nil
}

func (s *Service) Authenticate(token string) (Principal, error) {
	if token == "" {
		return Principal{}, gorm.ErrRecordNotFound
	}
	var session domain.Session
	if err := s.db.Where("token_hash = ? AND expires_at > ?", hashToken(token), time.Now().UTC()).First(&session).Error; err != nil {
		return Principal{}, err
	}
	var user domain.User
	if err := s.db.First(&user, "id = ? AND status = ?", session.UserID, "active").Error; err != nil {
		return Principal{}, err
	}
	var permissions []domain.Permission
	err := s.db.
		Table("permissions").
		Select("permissions.*").
		Joins("JOIN role_permissions ON role_permissions.permission_id = permissions.id").
		Joins("JOIN user_roles ON user_roles.role_id = role_permissions.role_id").
		Where("user_roles.user_id = ?", user.ID).
		Scan(&permissions).Error
	if err != nil {
		return Principal{}, err
	}
	codes := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		codes[permission.Code] = struct{}{}
	}
	return Principal{User: user, Permissions: codes, SessionID: session.ID}, nil
}

func (s *Service) Logout(token string) error {
	if token == "" {
		return nil
	}
	return s.db.Where("token_hash = ?", hashToken(token)).Delete(&domain.Session{}).Error
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
