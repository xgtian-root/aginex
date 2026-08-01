package tokenauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	accessTokenType = "access"
	bearerTokenType = "Bearer"
	maxJWTBytes     = 8 * 1024
)

var jwtHeader = struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}{
	Algorithm: "HS256",
	Type:      "JWT",
}

func signAccess(claims AccessClaims, key []byte) (string, error) {
	header, err := json.Marshal(jwtHeader)
	if err != nil {
		return "", fmt.Errorf("encode access header: %w", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode access claims: %w", err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." +
		base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyAccess(raw string, config Config, now time.Time) (AccessClaims, error) {
	if raw == "" || len(raw) > maxJWTBytes || strings.TrimSpace(raw) != raw {
		return AccessClaims{}, ErrInvalidAccess
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return AccessClaims{}, ErrInvalidAccess
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return AccessClaims{}, ErrInvalidAccess
	}
	var header struct {
		Algorithm string `json:"alg"`
		Type      string `json:"typ"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil ||
		header.Algorithm != jwtHeader.Algorithm ||
		header.Type != jwtHeader.Type {
		return AccessClaims{}, ErrInvalidAccess
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != sha256.Size {
		return AccessClaims{}, ErrInvalidAccess
	}
	mac := hmac.New(sha256.New, config.AccessSigningKey)
	_, _ = mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return AccessClaims{}, ErrInvalidAccess
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return AccessClaims{}, ErrInvalidAccess
	}
	var claims AccessClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return AccessClaims{}, ErrInvalidAccess
	}
	if claims.Issuer != config.Issuer ||
		claims.Audience != config.Audience ||
		claims.Type != accessTokenType ||
		!validID(claims.Subject) ||
		!validID(claims.TokenID) ||
		!validID(claims.FamilyID) ||
		!validID(claims.DeviceID) ||
		claims.IssuedAt <= 0 ||
		claims.ExpiresAt <= claims.IssuedAt {
		return AccessClaims{}, ErrInvalidAccess
	}
	now = utc(now)
	expiry := time.Unix(claims.ExpiresAt, 0).UTC().Add(config.ClockSkew)
	if !now.Before(expiry) {
		return AccessClaims{}, ErrAccessExpired
	}
	latestIssue := now.Add(config.ClockSkew).Unix()
	if claims.IssuedAt > latestIssue {
		return AccessClaims{}, ErrInvalidAccess
	}
	maxLifetimeSeconds := int64((config.AccessTTL + time.Second - 1) / time.Second)
	if claims.ExpiresAt-claims.IssuedAt > maxLifetimeSeconds {
		return AccessClaims{}, ErrInvalidAccess
	}
	return claims, nil
}
