// Package web embeds the built Intropy dashboard SPA so the CLI can serve it
// without any runtime dependency on Node or a Vite dev server.
//
// The embedded tree is the SPA's production build output (web/dist). A minimal
// placeholder dist/index.html is committed so `go build` and `go test` compile
// before the frontend has ever been built; a real `vite build` (see `make web`)
// overwrites it with the actual dashboard.
package web

import "embed"

// Assets holds the built SPA. It always contains dist/index.html; a full build
// adds the hashed JS/CSS bundles under dist/assets.
//
//go:embed all:dist
var Assets embed.FS
