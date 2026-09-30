package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
)

const (
	CSRFCookie    = "us_csrf"
	CSRFHeader    = "X-CSRF-Token"
	CSRFFormField = "csrf_token"
)

// CSRF implements the double-submit cookie: every request gets a token
// cookie; every unsafe request must echo it in the X-CSRF-Token header (htmx,
// via hx-headers in the layout) or the csrf_token form field. The cookie
// value is HMAC-signed so a subdomain cannot plant a chosen token.
func (a *Auth) CSRF(next http.Handler) http.Handler {
	key := a.cfg.SigningKey("csrf")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if c, err := r.Cookie(CSRFCookie); err == nil && validCSRF(key, c.Value) {
			token = c.Value
		} else {
			token = newCSRF(key)
			http.SetCookie(w, &http.Cookie{
				Name: CSRFCookie, Value: token, Path: "/",
				HttpOnly: true, Secure: a.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
			})
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			sent := r.Header.Get(CSRFHeader)
			if sent == "" {
				sent = r.PostFormValue(CSRFFormField)
			}
			if sent == "" || !hmac.Equal([]byte(sent), []byte(token)) {
				http.Error(w, "Invalid or missing CSRF token. Reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfKey, token)))
	})
}

func newCSRF(key []byte) string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	n := base64.RawURLEncoding.EncodeToString(b)
	return n + "." + sign(key, n)
}

func validCSRF(key []byte, v string) bool {
	n, sig, ok := strings.Cut(v, ".")
	return ok && hmac.Equal([]byte(sig), []byte(sign(key, n)))
}

func sign(key []byte, s string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
