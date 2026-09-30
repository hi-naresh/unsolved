package http

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/views/pages"
)

// mountAuth: sign-in chooser, provider start/callback, sign-out.
func (h *Handlers) mountAuth(r chi.Router) {
	r.Get("/signin", h.wrap(h.signInPage))
	r.With(h.limit(ActSignIn)).Get("/auth/{provider}/start", h.wrap(h.signInStart))
	r.Get("/auth/{provider}/callback", h.wrap(h.signInCallback))
	r.Post("/auth/signout", h.wrap(h.signOut))
}

func (h *Handlers) signInPage(w http.ResponseWriter, r *http.Request) error {
	next := auth.SafeNext(r.URL.Query().Get("next"))
	if auth.UserFrom(r.Context()) != nil {
		redirect(w, r, next)
		return nil
	}
	return h.render(w, r, http.StatusOK, pages.SignIn(next), nil)
}

func (h *Handlers) signInStart(w http.ResponseWriter, r *http.Request) error {
	p, ok := auth.ParseProvider(chi.URLParam(r, "provider"))
	if !ok || !h.Auth.Enabled(p) {
		return service.ErrNotFound
	}
	if err := h.Auth.StartSignIn(w, r, p, r.URL.Query().Get("next")); err != nil {
		h.Log.ErrorContext(r.Context(), "sign-in start", "provider", p, "err", err)
		return h.signInFailed(w, r)
	}
	return nil
}

func (h *Handlers) signInCallback(w http.ResponseWriter, r *http.Request) error {
	p, ok := auth.ParseProvider(chi.URLParam(r, "provider"))
	if !ok {
		return service.ErrNotFound
	}
	to, err := h.Auth.FinishSignIn(w, r, p)
	switch {
	case err == nil:
		http.Redirect(w, r, to, http.StatusSeeOther)
		return nil
	case auth.IsSignInFailure(err):
		return h.signInFailed(w, r)
	case errors.Is(err, service.ErrForbidden):
		return h.render(w, r, http.StatusForbidden,
			pages.Message("Can't sign in", "This account has been deleted."), nil)
	}
	return err
}

func (h *Handlers) signInFailed(w http.ResponseWriter, r *http.Request) error {
	return h.render(w, r, http.StatusBadRequest, pages.Message("Sign-in didn't work",
		"The sign-in link expired or was interrupted. Go back to Sign in and try again."), nil)
}

func (h *Handlers) signOut(w http.ResponseWriter, r *http.Request) error {
	if err := h.Auth.EndSession(w, r); err != nil {
		return err
	}
	redirect(w, r, "/")
	return nil
}
