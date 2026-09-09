// Package templates contains the CLI's self-contained, verified project snapshot.
package templates

import (
	"embed"
	"io/fs"
)

// BackendVersion pins the framework source consumed by generated projects.
// It resolves commit aa50cf7653440e75622aba46679a5272d4b5033e without a server release tag.
const BackendVersion = "v0.0.0-20260907062300-aa50cf765344"

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
