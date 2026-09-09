package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	installed := &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}
	for _, tc := range []struct {
		name, injected, want string
		info                 *debug.BuildInfo
	}{
		{name: "linker metadata wins", injected: " v1.2.4 ", info: installed, want: "v1.2.4"},
		{name: "go install version", info: installed, want: "v1.2.3"},
		{name: "no metadata", want: DevelopmentVersion},
		{name: "development module", info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, want: DevelopmentVersion},
		{name: "local VCS version", info: &debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}}}, want: DevelopmentVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveVersion(tc.injected, tc.info); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

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
