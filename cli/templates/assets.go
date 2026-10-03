// Package templates contains the CLI's self-contained, verified project snapshot.
package templates

import (
	"embed"
	"io/fs"
)

// BackendVersion pins the framework source consumed by generated projects.
// This published source includes the scaffold's captcha and public file-service
// contracts, Windows storage support, and patched security dependencies.
// The version was resolved by Go from commit
// 99a413b6a9e709bc8569f5289ffe9c55f15b729b. See docs/cli-release.md.
const BackendVersion = "v0.0.0-20261003134841-99a413b6a9e7"

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
