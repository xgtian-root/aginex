package buildinfo

import "testing"

func TestStringIncludesKnownCommit(t *testing.T) {
	previousVersion, previousCommit := Version, Commit
	t.Cleanup(func() {
		Version, Commit = previousVersion, previousCommit
	})
	Version = "v1.2.3"
	Commit = "abc123"
	if got := String(); got != "v1.2.3 (abc123)" {
		t.Fatalf("String() = %q", got)
	}

	Commit = "unknown"
	if got := String(); got != "v1.2.3" {
		t.Fatalf("String() with unknown commit = %q", got)
	}
}
