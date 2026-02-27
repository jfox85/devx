package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist
var embeddedFS embed.FS

func registerStaticRoutes(mux *http.ServeMux) {
	distFS, err := fs.Sub(embeddedFS, "dist")
	if err != nil {
		panic("embedded dist not found: " + err.Error())
	}
	mux.Handle("/", http.FileServer(http.FS(distFS)))
}
