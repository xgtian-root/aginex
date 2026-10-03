// Package templates contains the CLI's self-contained, verified project snapshot.
package templates

import (
	"embed"
	"io/fs"
)

// BackendVersion pins the framework source consumed by generated projects.
// This published source includes the captcha and public file-service contracts
// required by the scaffold. The version was resolved by Go from commit
// 929bc9e71b8ef3b1ea5291225c9d2d585267b5c4. See docs/cli-release.md.
const BackendVersion = "v0.0.0-20261003123916-929bc9e71b8e"

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
