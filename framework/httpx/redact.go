package httpx

import (
	"log/slog"
	"net/url"
	"strings"
	"unicode"
)

const RedactedValue = "[REDACTED]"

func RedactValue(key string, value any) any {
	if sensitiveLogKey(key) {
		return RedactedValue
	}
	// Driver, SDK, provider, and module errors may embed SQL, object keys,
	// signed URLs, or user input without retaining a field name that a generic
	// redactor can inspect. Keep structured operation/check names in logs and
	// redact the opaque error text conservatively.
	if _, ok := value.(error); ok {
		return RedactedValue
	}
	if text, ok := value.(string); ok && signedURL(text) {
		return RedactedValue
	}
	return value
}

func RedactFields(fields map[string]any) map[string]any {
	redacted := make(map[string]any, len(fields))
	for key, value := range fields {
		redacted[key] = RedactValue(key, value)
	}
	return redacted
}

func RedactAttr(_ []string, attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	if attr.Value.Kind() == slog.KindGroup {
		group := attr.Value.Group()
		redacted := make([]any, 0, len(group))
		for _, child := range group {
			redacted = append(redacted, RedactAttr(nil, child))
		}
		return slog.Group(attr.Key, redacted...)
	}
	if RedactValue(attr.Key, attr.Value.Any()) == RedactedValue {
		return slog.String(attr.Key, RedactedValue)
	}
	return attr
}

func sensitiveLogKey(key string) bool {
	normalized := normalizeLogKey(key)
	if normalized == "code" ||
		normalized == "otp" ||
		normalized == "pin" ||
		normalized == "authorization" {
		return true
	}
	for _, fragment := range []string{
		"password",
		"passwd",
		"passphrase",
		"token",
		"cookie",
		"secret",
		"verificationcode",
		"smscode",
		"signedurl",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func normalizeLogKey(key string) string {
	return strings.Map(func(value rune) rune {
		if unicode.IsLetter(value) || unicode.IsDigit(value) {
			return unicode.ToLower(value)
		}
		return -1
	}, key)
}

func signedURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.RawQuery == "" {
		return false
	}
	for key := range parsed.Query() {
		normalized := normalizeLogKey(key)
		if sensitiveLogKey(key) {
			return true
		}
		for _, signatureKey := range []string{
			"signature",
			"credential",
			"ossaccesskeyid",
			"googleaccessid",
			"xamzalgorithm",
			"xgoogalgorithm",
		} {
			if strings.Contains(normalized, signatureKey) {
				return true
			}
		}
	}
	return false
}
