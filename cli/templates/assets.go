// Package templates contains the CLI's self-contained, verified project snapshot.
package templates

import (
	"embed"
	"io/fs"
)

// BackendVersion pins the framework source consumed by generated projects.
// It resolves commit f6d57f4fe47fb451c35d19185a32a5504bff67a9 without a server release tag.
const BackendVersion = "v0.0.0-20260910062837-f6d57f4fe47f"

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
