package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var embeddedFS embed.FS

func registerStaticRoutes(mux *http.ServeMux) {
	distFS, err := fs.Sub(embeddedFS, "dist")
	if err != nil {
		panic("embedded dist not found: " + err.Error())
	}
	mux.Handle("/", spaHandler(distFS))
}

// spaHandler serves files from the embedded FS and falls back to index.html
// for any path that doesn't map to a real file, enabling client-side routing.
func spaHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Derive the FS-relative path (strip leading slash).
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		f, err := fsys.Open(path)
		if err != nil {
			// File not found — serve index.html for the SPA router.
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		f.Close()
		if strings.HasSuffix(path, ".webmanifest") {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		fileServer.ServeHTTP(w, r)
	})
}
