//go:build !panelembed

// Package panelweb is the panel's built web client. Without the panelembed
// build tag nothing is embedded: the API still works, and cmd/panel serves a
// build from disk when TORN_PANEL_STATIC_DIR names one (development).
package panelweb

import "io/fs"

// FS is nil: this binary carries no web client.
func FS() fs.FS { return nil }
