package http

import (
	"net/http"

	"github.com/a-h/templ"
)

// isHX reports whether the request came from htmx.
func isHX(r *http.Request) bool { return r.Header.Get("HX-Request") != "" }

// render writes page for normal requests and partial for htmx requests
// (falling back to page when partial is nil). Responses vary on HX-Request so
// a cache never serves a partial as a page.
func (h *Handlers) render(w http.ResponseWriter, r *http.Request, status int, page, partial templ.Component) error {
	c := page
	if isHX(r) && partial != nil {
		c = partial
	}
	w.Header().Add("Vary", "HX-Request")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	return c.Render(r.Context(), w)
}

// redirect sends the browser to url, using HX-Redirect for htmx requests.
func redirect(w http.ResponseWriter, r *http.Request, url string) {
	if isHX(r) {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}
