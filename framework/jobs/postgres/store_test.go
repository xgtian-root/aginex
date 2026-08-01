package postgres

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestNormalizePayloadCompactsAndHashes(t *testing.T) {
	payload, hash, err := normalizePayload([]byte("{\n  \"id\": \"file-1\"\n}"))
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"id":"file-1"}` {
		t.Fatalf("payload = %q", payload)
	}
	if len(hash) != 64 {
		t.Fatalf("hash length = %d", len(hash))
	}
}

func TestSafeFailureSummaryNeverPersistsOpaqueErrorContent(t *testing.T) {
	invalidUTF8 := string([]byte{0xff, 0xfe, 0xfd})
	message := strings.Repeat("密钥🔐", 2000) +
		"\r\n\t\x1b[31m" +
		"password=hunter2 bearer=secret signed-url=secret " +
		invalidUTF8

	got := safeFailureSummary(assertionError(message))
	if got != storedFailureSummary {
		t.Fatalf("safe failure summary = %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("safe failure summary is not valid UTF-8: %q", got)
	}
	for _, value := range got {
		if unicode.IsControl(value) {
			t.Fatalf("safe failure summary contains control rune %U", value)
		}
	}
	for _, secret := range []string{
		"hunter2",
		"bearer",
		"signed-url",
		"密钥",
		invalidUTF8,
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("safe failure summary leaked %q: %q", secret, got)
		}
	}
}

type assertionError string

func (err assertionError) Error() string {
	return string(err)
}
