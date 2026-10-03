// Package templates contains the CLI's self-contained, verified project snapshot.
package templates

import (
	"embed"
	"io/fs"
)

// BackendVersion pins the framework source consumed by generated projects.
// The current scaffold requires unpublished captcha and file-service contracts.
// This deliberately non-downloadable development marker disables remote project
// generation and release builds until a reviewed source commit is published and
// its real server module version is resolved with Go. Local --aginex-path use
// remains available. See docs/cli-release.md.
const BackendVersion = "v0.0.0-dev.unpublished"

//go:embed all:_project
var assets embed.FS

// ProjectTemplateFS exposes the scaffold without its packaging directory.
func ProjectTemplateFS() fs.FS {
	result, err := fs.Sub(assets, "_project")
	if err != nil {
		panic(err)
	}
	return result
}
