package web

import (
	"embed"
	"io/fs"
)

// dist is the built client: npm run build in this folder.
//
//go:embed dist
var dist embed.FS

func FS() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}
