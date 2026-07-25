package http

import (
	"embed"
	"io/fs"
	nethttp "net/http"
)

// uiStaticFS embeds the built Studio SPA (web/studio, `make studio-ui`). The
// directory always contains at least placeholder.txt so the embed compiles on
// checkouts that never built the frontend; spaIndex then reports false and
// the dashboard falls back to the legacy inline indexHTML.
//
//go:embed uistatic
var uiStaticFS embed.FS

// spaIndex returns the built SPA entry page when the frontend bundle exists.
func spaIndex() ([]byte, bool) {
	data, err := uiStaticFS.ReadFile("uistatic/index.html")
	if err != nil {
		return nil, false
	}
	return data, true
}

// spaAssets serves the built static assets (vite outputs assets/ next to
// index.html). It 404s naturally when the bundle was never built.
func spaAssets() nethttp.Handler {
	sub, err := fs.Sub(uiStaticFS, "uistatic")
	if err != nil {
		return nethttp.NotFoundHandler()
	}
	return nethttp.FileServer(nethttp.FS(sub))
}
