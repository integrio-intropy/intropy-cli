// Package web embeds the Intropy dashboard SPA so the CLI can serve it
// without any runtime dependency on Node or a Vite dev server.
//
// Two trees are embedded and one is picked at serve time: placeholder/dist,
// committed, and dist, the `vite build` output (see `make web`), which is
// gitignored and present only in a workspace where the SPA has been built.
// go:embed patterns must match at compile time, so an unbuilt workspace
// keeps a marker file in dist (restored by `make web-clean`). A built dist
// drops the marker, and its absence is what makes the real SPA win — the
// state released binaries ship.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:placeholder/dist all:dist
var embedded embed.FS

// Assets is the embedded SPA tree, holding index.html at its root with any
// hashed JS/CSS bundles under assets/. It is the real `vite build` output
// when the binary was compiled after `make web`, and the committed
// placeholder otherwise.
var Assets = pick()

func pick() fs.FS {
	// dist/index.html exists in both states, so presence alone does not
	// discriminate; the marker does. See placeholder/dist/index.html.
	if _, err := fs.Stat(embedded, "dist/placeholder.html"); err != nil {
		if sub, err := fs.Sub(embedded, "dist"); err == nil {
			return sub
		}
	}
	sub, err := fs.Sub(embedded, "placeholder/dist")
	if err != nil {
		// go:embed guarantees the pattern matched at compile time.
		panic("web: embedded placeholder missing")
	}
	return sub
}
