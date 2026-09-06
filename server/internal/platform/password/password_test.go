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
	if !ValidHash(encoded) {
		t.Fatal("generated password is not a structurally valid hash")
	}
	if Verify(encoded, "incorrect password") {
		t.Fatal("expected an incorrect password to fail")
	}
}

func TestHashAndVerifyAcceptPasswordsWithoutLengthBounds(t *testing.T) {
	for _, value := range []string{
		"x",
		"123456",
		"123456" + strings.Repeat("z", 2048),
	} {
		encoded, err := Hash(value)
		if err != nil {
			t.Fatalf("Hash password of length %d: %v", len(value), err)
		}
		if !Verify(encoded, value) {
			t.Fatalf("password of length %d did not verify", len(value))
		}
		if Verify(encoded, "") {
			t.Fatalf("empty password matched hash for length %d", len(value))
		}
	}
}

func TestHashRejectsEmptyPassword(t *testing.T) {
	if _, err := Hash(""); err == nil {
		t.Fatal("Hash accepted an empty password")
	}
}

func TestCredentialHashBoundsRejectUnsafeStoredCost(t *testing.T) {
	if Verify(
		"$argon2id$v=19$m=4294967295,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
		"correct horse battery staple",
	) {
		t.Fatal("Verify accepted an unsafe Argon2 memory cost")
	}
	if ValidHash(
		"$argon2id$v=19$m=4294967295,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2hoYXNoaGFzaA",
	) {
		t.Fatal("ValidHash accepted an unsafe Argon2 memory cost")
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

func TestVerifyDummyHandlesBodyBoundedInput(t *testing.T) {
	VerifyDummy(strings.Repeat("x", 2048))
	VerifyDummy("")
}
