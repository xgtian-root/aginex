package password

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	encoded, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(encoded, "correct horse battery staple") {
		t.Fatal("expected the password to verify")
	}
	if Verify(encoded, "incorrect password") {
		t.Fatal("expected an incorrect password to fail")
	}
}

func TestHashRejectsShortPassword(t *testing.T) {
	if _, err := Hash("short"); err == nil {
		t.Fatal("expected a short password to be rejected")
	}
}

func TestPasswordBoundsRejectOversizedInputAndUnsafeStoredCost(t *testing.T) {
	oversized := make([]byte, maxPasswordBytes+1)
	for index := range oversized {
		oversized[index] = 'a'
	}
	if _, err := Hash(string(oversized)); err == nil {
		t.Fatal("Hash accepted an oversized password")
	}
	if Verify(
		"$argon2id$v=19$m=4294967295,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"correct horse battery staple",
	) {
		t.Fatal("Verify accepted an unsafe Argon2 memory cost")
	}
	if Verify(
		"$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		string(oversized),
	) {
		t.Fatal("Verify accepted an oversized candidate password")
	}
}

func TestDummyCredentialUsesProductionArgon2Parameters(t *testing.T) {
	if !Verify(dummyCredentialHash, "aginex dummy password value") {
		t.Fatal("dummy credential is not a valid production password hash")
	}
	if Verify(dummyCredentialHash, "a different password value") {
		t.Fatal("dummy credential accepted an unrelated password")
	}
}

func TestVerifyDummyBoundsUntrustedInput(t *testing.T) {
	VerifyDummy(strings.Repeat("x", maxPasswordBytes+1))
	VerifyDummy("")
}
