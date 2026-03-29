package assets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embeddedAssets embed.FS

// FS returns the embedded assets filesystem.
func FS() fs.FS {
	return embeddedAssets
}
