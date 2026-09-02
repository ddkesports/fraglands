package web

import (
	"net/http"
)

// render executes one named template with the page data map.
func (w *Web) render(rw http.ResponseWriter, status int, name string, data any) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.WriteHeader(status)
	_ = w.templates.ExecuteTemplate(rw, name, data)
}

// renderError renders the typed error page.
func (w *Web) renderError(rw http.ResponseWriter, status int, message string) {
	w.render(rw, status, "error.html", map[string]any{
		"Message": message,
	})
}
