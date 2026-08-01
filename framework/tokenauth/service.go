package tokenauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

const refreshTokenPrefix = "ar1_"

// Service issues and validates access tokens and atomically rotates refresh
// tokens. It has no HTTP or browser-session behavior.
type Service struct {
	config   Config
	store    RefreshStore
	subjects SubjectLookup
	clock    Clock
	random   io.Reader
	randomMu sync.Mutex
}

// New constructs an opt-in token service without mutating schema.
func New(config Config, dependencies Dependencies) (*Service, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if nilInterface(dependencies.Store) {
		return nil, fmt.Errorf("%w: refresh store is required", ErrInvalidConfig)
	}
	if nilInterface(dependencies.Subjects) {
		return nil, fmt.Errorf("%w: subject lookup is required", ErrInvalidConfig)
	}
	if dependencies.Clock == nil {
		dependencies.Clock = wallClock{}
	}
	if nilInterface(dependencies.Clock) {
		return nil, fmt.Errorf("%w: clock is required", ErrInvalidConfig)
	}
	if dependencies.Random == nil {
		dependencies.Random = rand.Reader
	}
	if nilInterface(dependencies.Random) {
		return nil, fmt.Errorf("%w: random source is required", ErrInvalidConfig)
	}
	config.AccessSigningKey = append([]byte(nil), config.AccessSigningKey...)
	config.RefreshHashKey = append([]byte(nil), config.RefreshHashKey...)
	return &Service{
		config:   config,
		store:    dependencies.Store,
		subjects: dependencies.Subjects,
		clock:    dependencies.Clock,
		random:   dependencies.Random,
	}, nil
}

// Issue creates the first refresh token for a device family and a short-lived
// access token.
func (service *Service) Issue(
	ctx context.Context,
	userID string,
	device Device,
) (TokenPair, error) {
	if err := service.validateCall(ctx); err != nil {
		return TokenPair{}, err
	}
	if !validID(userID) {
		return TokenPair{}, fmt.Errorf("%w: user ID is required", ErrInvalidRequest)
	}
	if err := validateDevice(device); err != nil {
		return TokenPair{}, err
	}
	now, err := service.currentTime()
	if err != nil {
		return TokenPair{}, err
	}
	if _, err := service.activeSubject(ctx, userID); err != nil {
		return TokenPair{}, err
	}
	familyID, err := service.randomID("rf_")
	if err != nil {
		return TokenPair{}, err
	}
	tokenID, err := service.randomID("rt_")
	if err != nil {
		return TokenPair{}, err
	}
	rawRefresh, hash, err := service.newRefreshSecret()
	if err != nil {
		return TokenPair{}, err
	}
	accessID, err := service.randomID("at_")
	if err != nil {
		return TokenPair{}, err
	}
	refreshExpiry := now.Add(service.config.RefreshTTL)
	if _, err := service.store.Create(ctx, NewRefreshToken{
		ID:        tokenID,
		FamilyID:  familyID,
		UserID:    userID,
		Hash:      hash,
		Device:    cloneDevice(device),
		IssuedAt:  now,
		ExpiresAt: refreshExpiry,
	}); err != nil {
		return TokenPair{}, err
	}
	return service.tokenPair(userID, familyID, device.ID, accessID, rawRefresh, now, refreshExpiry)
}

// Refresh consumes one opaque refresh token. A used token is a replay signal:
// the store commits family revocation before ErrRefreshReplay is returned.
func (service *Service) Refresh(ctx context.Context, rawRefresh string) (TokenPair, error) {
	if err := service.validateCall(ctx); err != nil {
		return TokenPair{}, err
	}
	hash, err := service.hashRefresh(rawRefresh)
	if err != nil {
		return TokenPair{}, err
	}
	now, err := service.currentTime()
	if err != nil {
		return TokenPair{}, err
	}
	current, err := service.store.Lookup(ctx, hash)
	if err != nil {
		return TokenPair{}, err
	}
	if current.Status == RefreshStatusActive && current.ExpiresAt.After(now) {
		if _, err := service.activeSubject(ctx, current.UserID); err != nil {
			if errors.Is(err, ErrSubjectInactive) || errors.Is(err, ErrSubjectNotFound) {
				_, revokeErr := service.store.RevokeFamily(
					ctx,
					current.UserID,
					current.FamilyID,
					now,
					"subject_inactive",
				)
				if revokeErr != nil {
					return TokenPair{}, errors.Join(
						err,
						fmt.Errorf("revoke inactive subject family: %w", revokeErr),
					)
				}
			}
			return TokenPair{}, err
		}
	}

	// Invalid, used, revoked, and expired states are resolved atomically by the
	// store without consuming entropy for a replacement.
	if current.Status != RefreshStatusActive || !current.ExpiresAt.After(now) {
		_, rotateErr := service.store.Rotate(ctx, hash, RefreshReplacement{}, now)
		return TokenPair{}, rotateErr
	}

	replacementID, err := service.randomID("rt_")
	if err != nil {
		return TokenPair{}, err
	}
	nextRaw, nextHash, err := service.newRefreshSecret()
	if err != nil {
		return TokenPair{}, err
	}
	accessID, err := service.randomID("at_")
	if err != nil {
		return TokenPair{}, err
	}
	refreshExpiry := now.Add(service.config.RefreshTTL)
	rotated, err := service.store.Rotate(ctx, hash, RefreshReplacement{
		ID:        replacementID,
		Hash:      nextHash,
		IssuedAt:  now,
		ExpiresAt: refreshExpiry,
	}, now)
	if err != nil {
		return TokenPair{}, err
	}
	return service.tokenPair(
		rotated.UserID,
		rotated.FamilyID,
		rotated.Device.ID,
		accessID,
		nextRaw,
		now,
		refreshExpiry,
	)
}

// ValidateAccess verifies signature, issuer, audience, type, expiry, and the
// caller-owned current subject status.
func (service *Service) ValidateAccess(
	ctx context.Context,
	rawAccess string,
) (AuthenticatedSubject, error) {
	if err := service.validateCall(ctx); err != nil {
		return AuthenticatedSubject{}, err
	}
	now, err := service.currentTime()
	if err != nil {
		return AuthenticatedSubject{}, err
	}
	claims, err := verifyAccess(rawAccess, service.config, now)
	if err != nil {
		return AuthenticatedSubject{}, err
	}
	subject, err := service.activeSubject(ctx, claims.Subject)
	if err != nil {
		return AuthenticatedSubject{}, err
	}
	return AuthenticatedSubject{Subject: subject, Claims: claims}, nil
}

// RevokeFamily revokes one login/device family for the given user.
func (service *Service) RevokeFamily(
	ctx context.Context,
	userID string,
	familyID string,
) (int64, error) {
	if err := service.validateCall(ctx); err != nil {
		return 0, err
	}
	now, err := service.currentTime()
	if err != nil {
		return 0, err
	}
	return service.store.RevokeFamily(
		ctx,
		userID,
		familyID,
		now,
		"family_logout",
	)
}

// RevokeDevice revokes every active family identified by a caller-owned device
// ID for one user.
func (service *Service) RevokeDevice(
	ctx context.Context,
	userID string,
	deviceID string,
) (int64, error) {
	if err := service.validateCall(ctx); err != nil {
		return 0, err
	}
	now, err := service.currentTime()
	if err != nil {
		return 0, err
	}
	return service.store.RevokeDevice(
		ctx,
		userID,
		deviceID,
		now,
		"device_logout",
	)
}

// RevokeUser revokes every active refresh token for one user.
func (service *Service) RevokeUser(ctx context.Context, userID string) (int64, error) {
	if err := service.validateCall(ctx); err != nil {
		return 0, err
	}
	now, err := service.currentTime()
	if err != nil {
		return 0, err
	}
	return service.store.RevokeUser(
		ctx,
		userID,
		now,
		"all_devices_logout",
	)
}

func (service *Service) tokenPair(
	userID string,
	familyID string,
	deviceID string,
	accessID string,
	rawRefresh string,
	now time.Time,
	refreshExpiry time.Time,
) (TokenPair, error) {
	accessExpiry := now.Add(service.config.AccessTTL)
	claims := AccessClaims{
		Issuer:    service.config.Issuer,
		Audience:  service.config.Audience,
		Subject:   userID,
		ExpiresAt: accessExpiry.Unix(),
		IssuedAt:  now.Unix(),
		TokenID:   accessID,
		Type:      accessTokenType,
		FamilyID:  familyID,
		DeviceID:  deviceID,
	}
	rawAccess, err := signAccess(claims, service.config.AccessSigningKey)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken: AccessToken{
			Value:     rawAccess,
			Type:      bearerTokenType,
			ExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
		},
		RefreshToken: RefreshToken{
			Value:     rawRefresh,
			ExpiresAt: refreshExpiry,
		},
		FamilyID: familyID,
		DeviceID: deviceID,
	}, nil
}

func (service *Service) activeSubject(ctx context.Context, userID string) (Subject, error) {
	subject, err := service.subjects.LookupSubject(ctx, userID)
	if err != nil {
		return Subject{}, fmt.Errorf("lookup token subject: %w", err)
	}
	if subject.ID != userID {
		return Subject{}, fmt.Errorf("%w: lookup returned a different user", ErrInvalidSubject)
	}
	if !subject.Active {
		return Subject{}, ErrSubjectInactive
	}
	return subject, nil
}

func (service *Service) validateCall(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if service == nil ||
		nilInterface(service.store) ||
		nilInterface(service.subjects) ||
		nilInterface(service.clock) ||
		nilInterface(service.random) {
		return fmt.Errorf("%w: service is not initialized", ErrInvalidState)
	}
	return nil
}

func (service *Service) currentTime() (time.Time, error) {
	now := utc(service.clock.Now())
	if now.IsZero() || now.Unix() <= 0 || now.Year() >= 9999 {
		return time.Time{}, fmt.Errorf("%w: clock returned an unsupported time", ErrInvalidState)
	}
	return now, nil
}

func (service *Service) randomID(prefix string) (string, error) {
	value, err := service.randomBytes(16)
	if err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(value), nil
}

func (service *Service) newRefreshSecret() (string, TokenHash, error) {
	secret, err := service.randomBytes(32)
	if err != nil {
		return "", TokenHash{}, err
	}
	raw := refreshTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	return raw, service.keyedHash(raw), nil
}

func (service *Service) hashRefresh(raw string) (TokenHash, error) {
	if len(raw) != len(refreshTokenPrefix)+base64.RawURLEncoding.EncodedLen(32) ||
		len(raw) > 128 ||
		raw[:len(refreshTokenPrefix)] != refreshTokenPrefix {
		return TokenHash{}, ErrRefreshInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw[len(refreshTokenPrefix):])
	if err != nil || len(decoded) != 32 {
		return TokenHash{}, ErrRefreshInvalid
	}
	return service.keyedHash(raw), nil
}

func (service *Service) keyedHash(raw string) TokenHash {
	mac := hmac.New(sha256.New, service.config.RefreshHashKey)
	_, _ = mac.Write([]byte(raw))
	var hash TokenHash
	copy(hash[:], mac.Sum(nil))
	return hash
}

func (service *Service) randomBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	service.randomMu.Lock()
	defer service.randomMu.Unlock()
	if _, err := io.ReadFull(service.random, value); err != nil {
		return nil, fmt.Errorf("generate token entropy: %w", err)
	}
	return value, nil
}
