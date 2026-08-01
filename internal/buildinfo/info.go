// Package buildinfo exposes immutable release metadata injected at link time.
package buildinfo

import "strings"

var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// String returns a concise version suitable for CLIs and startup logs.
func String() string {
	version := strings.TrimSpace(Version)
	if version == "" {
		version = "unknown"
	}
	commit := strings.TrimSpace(Commit)
	if commit == "" || commit == "unknown" {
		return version
	}
	return version + " (" + commit + ")"
}
