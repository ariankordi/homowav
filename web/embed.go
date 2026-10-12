// Package web carries the built web UI inside the binary.
//
// The sources live next to this file (a Vite project). "npm run build" writes
// dist/, which is embedded here. A checkout that has not been built still
// compiles: Handler then serves a notice instead of the UI.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

const (
	// distDir is the Vite output directory inside the embedded tree.
	distDir = "dist"
	// indexFile is the single page every route falls back to.
	indexFile = "index.html"
	// assetsPrefix holds files Vite names by content hash, safe to cache forever.
	assetsPrefix = "assets/"
	// notBuiltNotice is served when dist/ has no index.html.
	notBuiltNotice = "hwclient: the web UI is not built. Run \"npm install && npm run build\" in web/, then rebuild.\n"
)

// Handler serves the UI. Unknown paths return the index page, so links into
// the single-page app work.
func Handler() http.Handler {
	dist, err := fs.Sub(embedded, distDir)
	if err != nil {
		panic(err) // The directory is embedded at compile time; this cannot fail.
	}
	return handlerFor(dist)
}

// handlerFor is Handler over any file tree, for tests.
func handlerFor(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(dist, indexFile); err != nil {
			http.Error(w, notBuiltNotice, http.StatusServiceUnavailable)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if info, err := fs.Stat(dist, name); name == "" || err != nil || info.IsDir() {
			// The index must always be revalidated, or a new build stays hidden.
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, dist, indexFile)
			return
		}
		if strings.HasPrefix(name, assetsPrefix) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}
