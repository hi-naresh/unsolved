package auth

import (
	"github.com/google/uuid"
	"net/http"
	"time"
)

// StartSession creates a session for userID and sets the us_session cookie
// (HttpOnly, Secure, SameSite=Lax, SESSION_TTL).
func (a *Auth) StartSession(w http.ResponseWriter, r *http.Request, userID uuid.UUID) error {
	token, expires, err := a.svc.NewSession(r.Context(), userID)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", Expires: expires,
		MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true,
		Secure: a.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// EndSession deletes the session row and clears the cookie.
func (a *Auth) EndSession(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(SessionCookie); err == nil {
		if err := a.svc.EndSession(r.Context(), c.Value); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode})
	return nil
}
