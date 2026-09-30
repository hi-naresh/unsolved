// Package auth owns sign-in (LinkedIn OIDC, X OAuth 2.0 PKCE), server-side
// sessions and CSRF. It reaches the database only through the service layer.
package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/service"
)

const SessionCookie = "us_session"

type Auth struct {
	cfg config.Config
	svc *service.Service
	log *slog.Logger
}

func New(cfg config.Config, svc *service.Service, log *slog.Logger) *Auth {
	return &Auth{cfg: cfg, svc: svc, log: log}
}

// LoadSession reads the us_session cookie and puts the viewer in the request
// context. Signed-out requests pass through with no user.
func (a *Auth) LoadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		u, err := a.svc.SessionUser(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, service.ErrNotFound) {
				a.log.ErrorContext(r.Context(), "load session", "err", err)
			}
			next.ServeHTTP(w, r)
			return
		}
		viewer := &User{
			ID: u.ID, Handle: u.Handle, DisplayName: u.DisplayName,
			Suspended: u.SuspendedAt != nil, IsAdmin: a.cfg.IsAdmin(u.Handle),
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), viewer)))
	})
}

// RequireUser sends signed-out visitors to sign in. htmx and non-GET requests
// get 401 with an HX-Redirect so the page navigates.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()) == nil {
			to := "/signin?next=" + url.QueryEscape(r.URL.RequestURI())
			if r.Method != http.MethodGet || r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", to)
				http.Error(w, "Sign in to do that.", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, to, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireWriter is RequireUser plus: suspended users cannot write.
func RequireWriter(next http.Handler) http.Handler {
	return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFrom(r.Context()).Suspended {
			http.Error(w, "Your account is suspended, so you can't post, vote or edit.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RequireAdmin allows only handles listed in ADMIN_HANDLES.
func RequireAdmin(next http.Handler) http.Handler {
	return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !UserFrom(r.Context()).IsAdmin {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
