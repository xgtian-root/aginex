package httpx

import "strings"

const (
	// SessionCookieName is the fixed browser-session cookie name published by
	// Aginex's OpenAPI contract.
	SessionCookieName = "aginex_session"
	// CSRFCookieName is the fixed double-submit CSRF cookie name.
	CSRFCookieName = "aginex_csrf"
	// CSRFHeaderName is the fixed request header paired with CSRFCookieName.
	CSRFHeaderName = "X-CSRF-Token"
)

var insecureCredentialMarkers = []string{
	"change-me",
	"changeme",
	"example-secret",
	"not-a-secret",
	"placeholder",
	"replace-me",
	"replace-with",
	"sample-secret",
	"todo-secret",
}

// CredentialLooksInsecure recognizes values that should never be accepted as
// production credentials. It intentionally catches checked-in placeholders,
// surrounding whitespace, repeated templates, and trivially low diversity
// without imposing a particular password alphabet.
func CredentialLooksInsecure(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return true
	}
	normalized := strings.ToLower(value)
	for _, marker := range insecureCredentialMarkers {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	if strings.Contains(normalized, "your-") &&
		strings.Contains(normalized, "-here") {
		return true
	}
	if repeatedCredentialTemplate(value) {
		return true
	}
	distinct := make(map[rune]struct{})
	for _, character := range value {
		distinct[character] = struct{}{}
	}
	return len(distinct) < 8
}

func repeatedCredentialTemplate(value string) bool {
	for width := 1; width <= len(value)/2; width++ {
		if len(value)%width != 0 {
			continue
		}
		if strings.Repeat(value[:width], len(value)/width) == value {
			return true
		}
	}
	return false
}
