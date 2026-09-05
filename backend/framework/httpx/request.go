package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

const (
	MaxRequestIDLength = 64
	MaxRedirectLength  = 2048
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// RequestID preserves a well-formed caller identifier and replaces every
// malformed value with an unpredictable server-generated identifier.
func RequestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 0 &&
		len(value) <= MaxRequestIDLength &&
		requestIDPattern.MatchString(value) {
		return value
	}

	var random [16]byte
	if _, err := rand.Read(random[:]); err == nil {
		return hex.EncodeToString(random[:])
	}
	// crypto/rand failures are exceptional. Keep a valid bounded value even
	// when the operating system random source is unavailable.
	return "request-id-unavailable"
}

// SafeLocalRedirect returns value only when it is a same-site absolute path.
// Encoded slash and backslash forms are checked after URL decoding as well.
func SafeLocalRedirect(value, fallback string) string {
	value = strings.TrimSpace(value)
	if !validLocalRedirect(value) {
		return fallback
	}
	return value
}

func validLocalRedirect(value string) bool {
	if value == "" || len(value) > MaxRedirectLength || containsControl(value) {
		return false
	}
	if !strings.HasPrefix(value, "/") ||
		strings.HasPrefix(value, "//") ||
		strings.Contains(value, `\`) {
		return false
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return false
	}

	decoded := value
	for range 2 {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return false
		}
		if next == decoded {
			break
		}
		decoded = next
	}
	if containsControl(decoded) ||
		!strings.HasPrefix(decoded, "/") ||
		strings.HasPrefix(decoded, "//") ||
		strings.Contains(decoded, `\`) {
		return false
	}
	return true
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
