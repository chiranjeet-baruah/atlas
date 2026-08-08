package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// ParseTemplates parses every embedded page/fragment template. Called once
// at router setup (web.New) and independently by each handler's tests.
func ParseTemplates() *template.Template {
	return template.Must(template.ParseFS(templatesFS, "templates/*.html"))
}

// StaticFS exposes the embedded static/ directory (style.css, htmx.min.js)
// as an http.FileSystem, rooted at static/ rather than the embed's own root
// — so a request for "/style.css" against this filesystem resolves to
// static/style.css on disk, not a not-found "static/style.css" lookup.
func StaticFS() http.FileSystem {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // unreachable: "static" is a directory embedded above
	}
	return http.FS(sub)
}
