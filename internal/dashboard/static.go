package dashboard

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/integrio-intropy/intropy-cli/web"
)

// staticHandler serves the embedded SPA. Real build assets (hashed JS/CSS) are
// served with their correct content types; unknown *routes* fall back to
// index.html so the SPA's client-side routing works on deep links and reloads.
//
// A missing *file* (a path with an extension, e.g. /assets/index-abc123.js) is
// a 404 rather than a fallback. Serving index.html there would answer a script
// request with HTML and a 200, which the browser rejects on its MIME check and
// reports only as an empty page — the failure mode when a binary is built
// without `make web`. A 404 names the missing file instead.
func staticHandler() (http.Handler, error) {
	sub, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded dashboard assets: %w", err)
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("embedded dashboard is missing index.html: %w", err)
	}
	fileServer := http.FileServerFS(sub)

	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if name == "." || name == "" || name == "index.html" {
			serveIndex(w)
			return
		}
		if _, err := fs.Stat(sub, name); err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			serveIndex(w)
			return
		}
		fileServer.ServeHTTP(w, r)
	}), nil
}
