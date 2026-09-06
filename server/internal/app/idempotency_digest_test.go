package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"
)

func TestRequestDigestIsStableKeyedAndNotRawBodyHash(t *testing.T) {
	request := httptest.NewRequest("PUT", "/api/v1/users/00000000-0000-0000-0000-000000000001/password", nil)
	request.Header.Set("Content-Type", "application/json")
	body := []byte(`{"password":"low entropy example"}`)

	first := requestDigest(request, body, "first high entropy deployment secret value")
	if first != requestDigest(request, body, "first high entropy deployment secret value") {
		t.Fatal("keyed request digest is not stable")
	}
	second := requestDigest(request, body, "second high entropy deployment secret value")
	if first == second {
		t.Fatal("request digest must differ across deployment secrets")
	}
	raw := sha256.Sum256(body)
	if first == hex.EncodeToString(raw[:]) {
		t.Fatal("request digest must not expose an unkeyed raw-body verifier")
	}
	if len(first) != sha256.Size*2 {
		t.Fatalf("request digest length = %d, want %d", len(first), sha256.Size*2)
	}
}
