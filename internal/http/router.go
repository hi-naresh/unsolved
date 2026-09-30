package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Router builds the public HTTP handler. Each resource file mounts its own
// routes via a mountX method so files stay independently owned.
func (h *Handlers) Router(staticDir string) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(realIP)
	r.Use(h.logRequests)
	r.Use(metricsMiddleware)
	r.Use(h.recoverPanics)
	r.Use(middleware.Timeout(15 * time.Second))
	r.Use(h.Auth.CSRF) // cheap (no DB), so it runs everywhere incl. the 404 page

	r.Get("/healthz", h.wrap(h.healthz))
	r.Handle("/static/*", staticFiles(staticDir))

	r.Group(func(r chi.Router) {
		r.Use(h.Auth.LoadSession)
		h.mountAuth(r)
		h.mountUsers(r)
		h.mountSettings(r)
		h.mountProblems(r)
		h.mountSolutions(r)
		h.mountReports(r)
		h.mountAdmin(r)
		h.mountPages(r)
		h.mountDiscovery(r)
	})
	r.NotFound(h.wrap(func(w http.ResponseWriter, r *http.Request) error { return errNotFoundPage }))
	return r
}

func staticFiles(dir string) http.Handler {
	fs := http.StripPrefix("/static/", http.FileServer(http.Dir(dir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fs.ServeHTTP(w, r)
	})
}
