package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const TraceParentHeader = "traceparent"

// TraceParent accepts only the W3C version 00 shape and starts a fresh trace
// when the caller supplies an invalid or unsupported value.
func TraceParent(candidate string) string {
	if normalized, ok := normalizeTraceParent(candidate); ok {
		return normalized
	}
	traceID := make([]byte, 16)
	parentID := make([]byte, 8)
	if _, err := rand.Read(traceID); err != nil {
		return ""
	}
	if _, err := rand.Read(parentID); err != nil {
		return ""
	}
	return "00-" + hex.EncodeToString(traceID) + "-" + hex.EncodeToString(parentID) + "-01"
}

func normalizeTraceParent(candidate string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(candidate))
	if len(value) != 55 ||
		value[2] != '-' ||
		value[35] != '-' ||
		value[52] != '-' ||
		value[:2] != "00" {
		return "", false
	}
	traceID := value[3:35]
	parentID := value[36:52]
	flags := value[53:55]
	if !isLowerHex(traceID) ||
		!isLowerHex(parentID) ||
		!isLowerHex(flags) ||
		allZeroHex(traceID) ||
		allZeroHex(parentID) {
		return "", false
	}
	return value, true
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func allZeroHex(value string) bool {
	for _, character := range value {
		if character != '0' {
			return false
		}
	}
	return true
}
