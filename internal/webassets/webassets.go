// Package webassets embeds the browser script built by `make web`.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var dist embed.FS

// Script returns the bundled airrbag.js, or nil when the binary was built
// without running `make web` first.
func Script() []byte {
	b, err := fs.ReadFile(dist, "dist/airrbag.js")
	if err != nil {
		return nil
	}
	return b
}
