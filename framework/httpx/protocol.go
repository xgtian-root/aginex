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

// UserPasswordLooksInsecure recognizes values that should never be accepted
// as user-chosen passwords. Request-size limits bound the value separately, so
// this check intentionally imposes no password length or character-diversity
// policy.
func UserPasswordLooksInsecure(value string) bool {
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
	return false
}

// CredentialLooksInsecure recognizes values that should never be accepted as
// production infrastructure credentials. In addition to placeholder and
// template checks, it requires enough diversity for secrets such as the
// server-side session key.
func CredentialLooksInsecure(value string) bool {
	if UserPasswordLooksInsecure(value) {
		return true
	}
	distinct := make(map[rune]struct{})
	for _, character := range value {
		distinct[character] = struct{}{}
	}
	return len(distinct) < 8
}

func repeatedCredentialTemplate(value string) bool {
	if len(value) < 2 {
		return false
	}
	prefix := make([]int, len(value))
	for index := 1; index < len(value); index++ {
		matched := prefix[index-1]
		for matched > 0 && value[index] != value[matched] {
			matched = prefix[matched-1]
		}
		if value[index] == value[matched] {
			matched++
		}
		prefix[index] = matched
	}
	period := len(value) - prefix[len(value)-1]
	return period < len(value) && len(value)%period == 0
}
