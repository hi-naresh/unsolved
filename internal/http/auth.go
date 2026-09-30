package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// mountAuth: sign-in chooser, provider start/callback, sign-out.
// Provider flows are implemented in phase 1.
func (h *Handlers) mountAuth(r chi.Router) {
	r.Post("/auth/signout", h.wrap(h.signOut))
}

func (h *Handlers) signOut(w http.ResponseWriter, r *http.Request) error {
	if err := h.Auth.EndSession(w, r); err != nil {
		return err
	}
	redirect(w, r, "/")
	return nil
}
