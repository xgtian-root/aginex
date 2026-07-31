package httpx

import (
	"regexp"
	"strings"
	"testing"
)

func TestRequestIDPreservesOnlyBoundedSafeValues(t *testing.T) {
	const valid = "01J.request_ID:abc-123"
	if got := RequestID(" " + valid + " "); got != valid {
		t.Fatalf("RequestID() = %q, want %q", got, valid)
	}

	cases := []string{
		"",
		strings.Repeat("a", MaxRequestIDLength+1),
		"contains a space",
		"line\nbreak",
		"quote'break",
	}
	pattern := regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	for _, value := range cases {
		t.Run(value, func(t *testing.T) {
			got := RequestID(value)
			if got == value {
				t.Fatalf("unsafe request ID was preserved: %q", value)
			}
			if !pattern.MatchString(got) {
				t.Fatalf("generated request ID = %q", got)
			}
		})
	}
}

func TestSafeLocalRedirect(t *testing.T) {
	const fallback = "/dashboard"
	valid := []string{
		"/files",
		"/products?page=2",
		"/audit#latest",
	}
	for _, value := range valid {
		t.Run("valid "+value, func(t *testing.T) {
			if got := SafeLocalRedirect(value, fallback); got != value {
				t.Fatalf("redirect = %q, want %q", got, value)
			}
		})
	}

	invalid := []string{
		"",
		"https://evil.example",
		"javascript:alert(1)",
		"//evil.example/path",
		`/\evil.example`,
		"/%5cevil.example",
		"/%2fevil.example",
		"/%252fevil.example",
		"/safe\nLocation: https://evil.example",
		strings.Repeat("a", MaxRedirectLength+1),
	}
	for _, value := range invalid {
		t.Run("invalid "+value, func(t *testing.T) {
			if got := SafeLocalRedirect(value, fallback); got != fallback {
				t.Fatalf("redirect = %q, want fallback %q", got, fallback)
			}
		})
	}
}
