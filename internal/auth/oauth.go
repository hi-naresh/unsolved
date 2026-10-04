package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hi-naresh/unsolved/internal/store"
	"golang.org/x/oauth2"
)

// StateCookie carries the OAuth state, PKCE verifier and post-sign-in path
// between /auth/{provider}/start and the callback. HMAC-signed, 10 minutes.
const (
	StateCookie = "us_oauth"
	stateTTL    = 10 * time.Minute
	statePath   = "/auth/"
)

var (
	// ErrSignInFailed covers a bad or expired state, a provider error or a
	// failed exchange. The visitor should simply try again.
	ErrSignInFailed    = errors.New("sign-in failed")
	errUnknownProvider = errors.New("unknown provider")
)

// ParseProvider maps a route segment to a provider. ok is false for anything
// but supported sign-in providers.
func ParseProvider(s string) (store.Provider, bool) {
	p := store.Provider(s)
	switch p {
	case store.ProviderLinkedin, store.ProviderX, store.ProviderGoogle, store.ProviderGithub, store.ProviderReddit:
		return p, true
	}
	return "", false
}

// SafeNext returns next if it is a local path, else "/". Protocol-relative
// ("//host") and backslash tricks are rejected so sign-in can't be used as an
// open redirect.
func SafeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") ||
		strings.ContainsAny(next, "\\\r\n\t") {
		return "/"
	}
	u, err := url.Parse(next)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return next
}

type oauthState struct {
	Provider string `json:"p"`
	State    string `json:"s"`
	Verifier string `json:"v"`
	Next     string `json:"n"`
	Expires  int64  `json:"e"`
}

// StartSignIn redirects to the provider's consent page, remembering state,
// PKCE verifier and next in a signed cookie.
func (a *Auth) StartSignIn(w http.ResponseWriter, r *http.Request, provider store.Provider, next string) error {
	conf, err := a.oauthConfig(r.Context(), provider)
	if err != nil {
		return err
	}
	st := oauthState{
		Provider: string(provider), State: randToken(24), Verifier: oauth2.GenerateVerifier(),
		Next: SafeNext(next), Expires: time.Now().Add(stateTTL).Unix(),
	}
	http.SetCookie(w, &http.Cookie{
		Name: StateCookie, Value: a.encodeState(st), Path: statePath, MaxAge: int(stateTTL.Seconds()),
		HttpOnly: true, Secure: a.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, conf.AuthCodeURL(st.State, oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
	return nil
}

// FinishSignIn handles the provider callback: checks state, exchanges the
// code, signs the person in (creating the user on first sign-in) and starts
// a session. It returns where to send them: /welcome for new users.
// Errors: ErrSignInFailed for anything the visitor can retry; ErrForbidden
// from the service for a deleted account.
func (a *Auth) FinishSignIn(w http.ResponseWriter, r *http.Request, provider store.Provider) (to string, err error) {
	ctx := r.Context()
	st, ok := a.readState(r)
	// The state cookie is single-use whatever happens next.
	http.SetCookie(w, &http.Cookie{Name: StateCookie, Value: "", Path: statePath, MaxAge: -1,
		HttpOnly: true, Secure: a.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	if !ok || st.Provider != string(provider) || q.Get("state") == "" ||
		!hmac.Equal([]byte(q.Get("state")), []byte(st.State)) {
		a.log.WarnContext(ctx, "oauth callback: bad state", "provider", provider)
		return "", ErrSignInFailed
	}
	if e := q.Get("error"); e != "" || q.Get("code") == "" {
		a.log.InfoContext(ctx, "oauth callback: provider returned no code", "provider", provider, "error", e)
		return "", ErrSignInFailed
	}
	id, err := a.fetchIdentity(ctx, provider, q.Get("code"), st.Verifier)
	if err != nil {
		a.log.WarnContext(ctx, "oauth callback: identity fetch failed", "provider", provider, "err", err)
		return "", ErrSignInFailed
	}
	u, created, err := a.svc.SignInWithIdentity(ctx, id.Provider, id.UID, id.ProfileURL, id.DisplayName)
	if err != nil {
		return "", err
	}
	if err := a.StartSession(w, r, u.ID); err != nil {
		return "", err
	}
	next := SafeNext(st.Next)
	if created {
		return "/welcome?next=" + url.QueryEscape(next), nil
	}
	return next, nil
}

// IsSignInFailure reports whether err is a retryable sign-in failure.
func IsSignInFailure(err error) bool {
	return errors.Is(err, ErrSignInFailed) || errors.Is(err, errUnknownProvider)
}

func (a *Auth) encodeState(st oauthState) string {
	b, _ := json.Marshal(st)
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + sign(a.cfg.SigningKey("oauth_state"), p)
}

func (a *Auth) readState(r *http.Request) (oauthState, bool) {
	var st oauthState
	c, err := r.Cookie(StateCookie)
	if err != nil {
		return st, false
	}
	p, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(sign(a.cfg.SigningKey("oauth_state"), p))) {
		return st, false
	}
	b, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return st, false
	}
	if time.Now().Unix() > st.Expires || st.State == "" || st.Verifier == "" {
		return st, false
	}
	return st, true
}

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
