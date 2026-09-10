// Package buildinfo exposes immutable release metadata injected at link time.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

const DevelopmentVersion = "0.1.1-dev"

var (
	Version   = ""
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String returns a concise version suitable for CLIs and startup logs.
func String() string {
	info, _ := debug.ReadBuildInfo()
	version := resolveVersion(Version, info)
	commit := strings.TrimSpace(Commit)
	if commit == "" || commit == "unknown" {
		return version
	}
	return version + " (" + commit + ")"
}

func resolveVersion(injected string, info *debug.BuildInfo) string {
	if version := strings.TrimSpace(injected); version != "" {
		return version
	}
	if info != nil {
		// Go 1.24+ also assigns module versions to local VCS builds. Keep those
		// identifiable as development builds unless the release script stamped them.
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" || setting.Key == "vcs.modified" {
				return DevelopmentVersion
			}
		}
		if version := strings.TrimSpace(info.Main.Version); version != "" && version != "(devel)" {
			return version
		}
	}
	return DevelopmentVersion
}
