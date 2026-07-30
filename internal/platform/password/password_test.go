package password

import "testing"

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
