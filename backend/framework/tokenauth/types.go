package tokenauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	RefreshStatusActive  = "active"
	RefreshStatusUsed    = "used"
	RefreshStatusRevoked = "revoked"

	maxIdentifierBytes = 191
	maxIssuerBytes     = 512
	maxAudienceBytes   = 256
	maxDeviceNameBytes = 256
	maxPlatformBytes   = 64
	maxMetadataEntries = 32
	maxMetadataKey     = 64
	maxMetadataValue   = 512
	maxMetadataJSON    = 8 * 1024
	minKeyBytes        = 32
	minAccessTTL       = time.Second
	maxAccessTTL       = 24 * time.Hour
	maxRefreshTTL      = 366 * 24 * time.Hour
	maxClockSkew       = 5 * time.Minute
)

var (
	ErrInvalidConfig      = errors.New("invalid token authentication config")
	ErrInvalidRequest     = errors.New("invalid token authentication request")
	ErrInvalidIdentity    = errors.New("invalid external identity")
	ErrIdentityNotFound   = errors.New("external identity is not linked")
	ErrIdentityInactive   = errors.New("external identity is inactive")
	ErrInvalidSubject     = errors.New("invalid token subject")
	ErrSubjectNotFound    = errors.New("token subject not found")
	ErrSubjectInactive    = errors.New("token subject is inactive")
	ErrInvalidAccess      = errors.New("invalid access token")
	ErrAccessExpired      = errors.New("access token expired")
	ErrRefreshInvalid     = errors.New("invalid refresh token")
	ErrRefreshExpired     = errors.New("refresh token expired")
	ErrRefreshRevoked     = errors.New("refresh token revoked")
	ErrRefreshReplay      = errors.New("refresh token replay detected")
	ErrInvalidState       = errors.New("invalid token authentication state")
	ErrDatabaseRequired   = errors.New("token authentication database is required")
	ErrUnsupportedDialect = errors.New("unsupported token authentication migration dialect")
)

// Clock supports deterministic issuance and expiry checks.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a function to Clock.
type ClockFunc func() time.Time

func (function ClockFunc) Now() time.Time {
	return function()
}

type wallClock struct{}

func (wallClock) Now() time.Time {
	return time.Now()
}

// Subject is the caller-owned user/status projection needed by this module.
// The adapter maps application-specific states into Active.
type Subject struct {
	ID     string
	Status string
	Active bool
}

// SubjectLookup keeps application user models and status rules outside the
// token module.
type SubjectLookup interface {
	LookupSubject(context.Context, string) (Subject, error)
}

// Device identifies the client family receiving refresh tokens. Metadata is
// bounded operational context, not a place for credentials or secrets.
type Device struct {
	ID       string
	Name     string
	Platform string
	Metadata map[string]string
}

// AccessToken is returned only to the caller; Value is never persisted by this
// module.
type AccessToken struct {
	Value     string    `json:"accessToken"`
	Type      string    `json:"tokenType"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// RefreshToken is returned once. Its Value is opaque; only a keyed hash is
// persisted.
type RefreshToken struct {
	Value     string    `json:"refreshToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// TokenPair is the transport-neutral result of issuance or rotation.
type TokenPair struct {
	AccessToken  AccessToken  `json:"access"`
	RefreshToken RefreshToken `json:"refresh"`
	FamilyID     string       `json:"familyId"`
	DeviceID     string       `json:"deviceId"`
}

// AccessClaims are the signed, security-relevant JWT fields.
type AccessClaims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
	IssuedAt  int64  `json:"iat"`
	TokenID   string `json:"jti"`
	Type      string `json:"typ"`
	FamilyID  string `json:"fid"`
	DeviceID  string `json:"did"`
}

// AuthenticatedSubject combines verified access claims with the caller-owned
// subject/status projection.
type AuthenticatedSubject struct {
	Subject Subject
	Claims  AccessClaims
}

// Config pins token trust boundaries. Signing and refresh-hash keys must be
// distinct values of at least 256 bits.
type Config struct {
	Issuer           string
	Audience         string
	AccessTTL        time.Duration
	RefreshTTL       time.Duration
	ClockSkew        time.Duration
	AccessSigningKey []byte
	RefreshHashKey   []byte
}

// Dependencies are explicit so tests and applications control persistence,
// status lookup, time, and entropy.
type Dependencies struct {
	Store    RefreshStore
	Subjects SubjectLookup
	Clock    Clock
	Random   io.Reader
}

func validateConfig(config Config) error {
	if !validText(config.Issuer, maxIssuerBytes) {
		return fmt.Errorf("%w: issuer is required and must be bounded text", ErrInvalidConfig)
	}
	if !validText(config.Audience, maxAudienceBytes) {
		return fmt.Errorf("%w: audience is required and must be bounded text", ErrInvalidConfig)
	}
	if config.AccessTTL < minAccessTTL || config.AccessTTL > maxAccessTTL {
		return fmt.Errorf(
			"%w: access TTL must be between %s and %s",
			ErrInvalidConfig,
			minAccessTTL,
			maxAccessTTL,
		)
	}
	if config.RefreshTTL < config.AccessTTL || config.RefreshTTL > maxRefreshTTL {
		return fmt.Errorf(
			"%w: refresh TTL must be at least the access TTL and at most %s",
			ErrInvalidConfig,
			maxRefreshTTL,
		)
	}
	if config.ClockSkew < 0 || config.ClockSkew > maxClockSkew {
		return fmt.Errorf("%w: clock skew must be between zero and %s", ErrInvalidConfig, maxClockSkew)
	}
	if len(config.AccessSigningKey) < minKeyBytes {
		return fmt.Errorf("%w: access signing key must contain at least %d bytes", ErrInvalidConfig, minKeyBytes)
	}
	if len(config.RefreshHashKey) < minKeyBytes {
		return fmt.Errorf("%w: refresh hash key must contain at least %d bytes", ErrInvalidConfig, minKeyBytes)
	}
	if bytes.Equal(config.AccessSigningKey, config.RefreshHashKey) {
		return fmt.Errorf("%w: signing and refresh-hash keys must be distinct", ErrInvalidConfig)
	}
	return nil
}

func validateDevice(device Device) error {
	if !validText(device.ID, maxIdentifierBytes) {
		return fmt.Errorf("%w: device ID is required and must be bounded text", ErrInvalidRequest)
	}
	if device.Name != "" && !validText(device.Name, maxDeviceNameBytes) {
		return fmt.Errorf("%w: device name must be bounded text", ErrInvalidRequest)
	}
	if device.Platform != "" && !validText(device.Platform, maxPlatformBytes) {
		return fmt.Errorf("%w: device platform must be bounded text", ErrInvalidRequest)
	}
	if len(device.Metadata) > maxMetadataEntries {
		return fmt.Errorf("%w: device metadata has too many entries", ErrInvalidRequest)
	}
	for key, value := range device.Metadata {
		if !validText(key, maxMetadataKey) || !validText(value, maxMetadataValue) {
			return fmt.Errorf("%w: device metadata must contain bounded text", ErrInvalidRequest)
		}
	}
	return nil
}

func validID(value string) bool {
	return validText(value, maxIdentifierBytes)
}

func validText(value string, maximum int) bool {
	if value == "" ||
		len(value) > maximum ||
		!utf8.ValidString(value) ||
		strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func cloneDevice(device Device) Device {
	cloned := Device{
		ID:       device.ID,
		Name:     device.Name,
		Platform: device.Platform,
		Metadata: make(map[string]string, len(device.Metadata)),
	}
	for key, value := range device.Metadata {
		cloned.Metadata[key] = value
	}
	return cloned
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func utc(value time.Time) time.Time {
	return value.UTC().Round(0)
}
