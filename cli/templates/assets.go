// Package templates contains the CLI's self-contained, verified project snapshot.
package templates

import (
	"embed"
	"io/fs"
)

// BackendVersion is the backend release bundled by this CLI, independent of its version.
const BackendVersion = "0.1.0-dev"

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
