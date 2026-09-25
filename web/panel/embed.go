//go:build panelembed

// Package panelweb is the panel's built web client, embedded into the
// binary when it is built with -tags panelembed after `npm run build`.
package panelweb

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS is the built client, index.html at its root.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
