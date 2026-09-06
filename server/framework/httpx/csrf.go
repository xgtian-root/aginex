package httpx

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const CSRFTokenBytes = 32

type CSRFConfig struct {
	TokenCookieName      string
	HeaderName           string
	SessionCookieNames   []string
	AllowedOrigins       []string
	AllowBearer          bool
	AllowUnauthenticated bool
}

func GenerateCSRFToken() (string, error) {
	token := make([]byte, CSRFTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("generate CSRF token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

// ReuseOrGenerateCSRFToken keeps the browser's double-submit token stable
// across tabs. Rotating it on every read would invalidate the in-memory token
// held by every other open tab even though they share the same cookie jar.
func ReuseOrGenerateCSRFToken(
	request *http.Request,
	cookieName string,
) (string, error) {
	if request != nil {
		if cookie, err := request.Cookie(cookieName); err == nil &&
			validCSRFToken(cookie.Value) {
			return cookie.Value, nil
		}
	}
	return GenerateCSRFToken()
}

func EqualCSRFToken(left, right string) bool {
	leftToken, leftErr := base64.RawURLEncoding.DecodeString(left)
	rightToken, rightErr := base64.RawURLEncoding.DecodeString(right)
	if leftErr != nil ||
		rightErr != nil ||
		len(leftToken) != CSRFTokenBytes ||
		len(rightToken) != CSRFTokenBytes {
		return false
	}
	return subtle.ConstantTimeCompare(leftToken, rightToken) == 1
}

func validCSRFToken(value string) bool {
	token, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(token) == CSRFTokenBytes
}

func NewCSRF(config CSRFConfig) (gin.HandlerFunc, error) {
	config.TokenCookieName = strings.TrimSpace(config.TokenCookieName)
	config.HeaderName = strings.TrimSpace(config.HeaderName)
	if config.TokenCookieName == "" {
		return nil, fmt.Errorf("CSRF token cookie name is required")
	}
	if config.HeaderName == "" {
		return nil, fmt.Errorf("CSRF header name is required")
	}
	if len(config.SessionCookieNames) == 0 {
		return nil, fmt.Errorf("at least one session cookie name is required")
	}
	sessionCookies := make([]string, 0, len(config.SessionCookieNames))
	seenCookies := make(map[string]struct{}, len(config.SessionCookieNames))
	for _, name := range config.SessionCookieNames {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("session cookie names must not be empty")
		}
		if _, exists := seenCookies[name]; exists {
			return nil, fmt.Errorf("duplicate session cookie name %q", name)
		}
		seenCookies[name] = struct{}{}
		sessionCookies = append(sessionCookies, name)
	}
	allowlist, err := buildOriginAllowlist(config.AllowedOrigins)
	if err != nil {
		return nil, fmt.Errorf("configure CSRF origins: %w", err)
	}

	return func(c *gin.Context) {
		if !unsafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		hasSessionCookie := false
		for _, name := range sessionCookies {
			if _, err := c.Request.Cookie(name); err == nil {
				hasSessionCookie = true
				break
			}
		}

		if !hasSessionCookie && validBearer(c.GetHeader("Authorization")) {
			if !config.AllowBearer ||
				!sourceHeadersAllowed(
					c.GetHeader("Origin"),
					c.GetHeader("Referer"),
					allowlist,
					false,
				) {
				denyCSRF(c)
				return
			}
			c.Next()
			return
		}

		if !hasSessionCookie && !config.AllowUnauthenticated {
			denyCSRF(c)
			return
		}
		if !sourceHeadersAllowed(
			c.GetHeader("Origin"),
			c.GetHeader("Referer"),
			allowlist,
			true,
		) {
			denyCSRF(c)
			return
		}
		cookieToken, err := c.Cookie(config.TokenCookieName)
		if err != nil || !EqualCSRFToken(cookieToken, c.GetHeader(config.HeaderName)) {
			denyCSRF(c)
			return
		}
		c.Next()
	}, nil
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	default:
		return true
	}
}

func validBearer(value string) bool {
	parts := strings.Fields(value)
	return len(parts) == 2 &&
		strings.EqualFold(parts[0], "Bearer") &&
		parts[1] != ""
}

func denyCSRF(c *gin.Context) {
	AbortProblem(
		c,
		http.StatusForbidden,
		"CSRF_FORBIDDEN",
		"Request denied",
		"CSRF token or request origin validation failed.",
	)
}
