// Package webassets embeds the browser code built by `make web` (the script
// injected into the *Arr UI and the dashboard bundle) and the static icons.
package webassets

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

//go:embed dist/* static/*
var files embed.FS

// Script returns the bundled airrbag.js, or nil when the binary was built
// without running `make web` first.
func Script() []byte {
	b, err := fs.ReadFile(files, "dist/airrbag.js")
	if err != nil {
		return nil
	}
	return b
}

var types = map[string]string{
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".webmanifest": "application/manifest+json",
}

// Asset returns a dashboard file by its public name: "dashboard.js",
// "dashboard.css" (built), or "static/<icon>" (committed). Only names with a
// known type are served; anything else is reported missing.
func Asset(name string) (data []byte, contentType string, ok bool) {
	name = path.Clean("/" + name)[1:]
	ct, known := types[path.Ext(name)]
	if !known || strings.Contains(name, "..") {
		return nil, "", false
	}
	p := "dist/" + name
	if strings.HasPrefix(name, "static/") {
		p = name
	} else if strings.Contains(name, "/") {
		return nil, "", false
	}
	b, err := fs.ReadFile(files, p)
	if err != nil {
		return nil, "", false
	}
	return b, ct, true
}
