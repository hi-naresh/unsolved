package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hi-naresh/unsolved/internal/views/pages"
)

// mountPages: the static-content pages /privacy and /about.
func (h *Handlers) mountPages(r chi.Router) {
	r.Get("/privacy", h.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return h.render(w, r, http.StatusOK, pages.Privacy(), nil)
	}))
	r.Get("/about", h.wrap(func(w http.ResponseWriter, r *http.Request) error {
		return h.render(w, r, http.StatusOK, pages.About(), nil)
	}))
}
