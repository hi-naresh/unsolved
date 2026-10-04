package http

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store"
)

// fakeProviders is an httptest server playing LinkedIn (an OIDC issuer with
// discovery, JWKS and RS256 ID tokens) and X (OAuth 2.0 PKCE + users/me).
// Tests register what identity a code should resolve to with grant.
type fakeProviders struct {
	t        *testing.T
	srv      *httptest.Server
	key      *rsa.PrivateKey
	wrongKey *rsa.PrivateKey // signs tokens when badSignature is set

	mu           sync.Mutex
	codes        map[string]fakeGrant // code → grant
	tokens       map[string]fakeGrant // X access token → grant
	badSignature bool
}

type fakeGrant struct {
	challenge string // PKCE S256 code_challenge from the authorize URL
	uid       string
	name      string
	username  string // X only
	issuer    string
	audience  string
	expired   bool
}

func newFakeProviders(t *testing.T) *fakeProviders {
	t.Helper()
	f := &fakeProviders{t: t, codes: map[string]fakeGrant{}, tokens: map[string]fakeGrant{}}
	var err error
	if f.key, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	if f.wrongKey, err = rsa.GenerateKey(rand.Reader, 2048); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /li/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		iss := f.srv.URL + "/li"
		authWriteJSON(w, map[string]any{
			"issuer":                                iss,
			"authorization_endpoint":                iss + "/authorize",
			"token_endpoint":                        iss + "/token",
			"jwks_uri":                              iss + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"pairwise"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"scopes_supported":                      []string{"openid", "profile"},
		})
	})
	mux.HandleFunc("GET /li/jwks", func(w http.ResponseWriter, r *http.Request) {
		authWriteJSON(w, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": authB64(f.key.N.Bytes()), "e": authB64(big.NewInt(int64(f.key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("GET /google/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		iss := f.srv.URL + "/google"
		authWriteJSON(w, map[string]any{"issuer": iss, "authorization_endpoint": iss + "/authorize", "token_endpoint": iss + "/token", "jwks_uri": iss + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("GET /google/jwks", func(w http.ResponseWriter, r *http.Request) {
		authWriteJSON(w, map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig", "n": authB64(f.key.N.Bytes()), "e": authB64(big.NewInt(int64(f.key.E)).Bytes())}}})
	})
	mux.HandleFunc("POST /li/token", func(w http.ResponseWriter, r *http.Request) {
		// LinkedIn: client credentials in the form body.
		if r.PostFormValue("client_id") != "li-client" || r.PostFormValue("client_secret") != "li-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		g, ok := f.redeem(r)
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		authWriteJSON(w, map[string]any{
			"access_token": "li-access", "token_type": "Bearer", "expires_in": 3600,
			"id_token": f.idToken(g, "linkedin"),
		})
	})
	mux.HandleFunc("POST /google/token", func(w http.ResponseWriter, r *http.Request) {
		if r.PostFormValue("client_id") != "google-client" || r.PostFormValue("client_secret") != "google-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		g, ok := f.redeem(r)
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		authWriteJSON(w, map[string]any{"access_token": "google-access", "token_type": "Bearer", "expires_in": 3600, "id_token": f.idToken(g, "google")})
	})
	mux.HandleFunc("POST /x/token", func(w http.ResponseWriter, r *http.Request) {
		// X confidential client: HTTP Basic.
		if id, secret, ok := r.BasicAuth(); !ok || id != "x-client" || secret != "x-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		g, ok := f.redeem(r)
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		tok := "x-access-" + g.uid
		f.mu.Lock()
		f.tokens[tok] = g
		f.mu.Unlock()
		authWriteJSON(w, map[string]any{"access_token": tok, "token_type": "bearer", "expires_in": 7200})
	})
	mux.HandleFunc("GET /x/users/me", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		g, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		authWriteJSON(w, map[string]any{"data": map[string]string{"id": g.uid, "name": g.name, "username": g.username}})
	})
	mux.HandleFunc("POST /github/token", func(w http.ResponseWriter, r *http.Request) {
		if r.PostFormValue("client_id") != "github-client" || r.PostFormValue("client_secret") != "github-secret" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		g, ok := f.redeem(r)
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		tok := "github-access-" + g.uid
		f.mu.Lock()
		f.tokens[tok] = g
		f.mu.Unlock()
		authWriteJSON(w, map[string]any{"access_token": tok, "token_type": "bearer"})
	})
	mux.HandleFunc("GET /github/user", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		g, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		authWriteJSON(w, map[string]any{"id": g.uid, "login": g.username, "name": g.name})
	})
	mux.HandleFunc("POST /reddit/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "reddit-client" || secret != "reddit-secret" || r.Header.Get("User-Agent") != "web:unsolved-tests:v1 (by /u/test_operator)" {
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
			return
		}
		g, ok := f.redeem(r)
		if !ok {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		tok := "reddit-access-" + g.uid
		f.mu.Lock()
		f.tokens[tok] = g
		f.mu.Unlock()
		authWriteJSON(w, map[string]any{"access_token": tok, "token_type": "bearer"})
	})
	mux.HandleFunc("GET /reddit/me", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		g, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		if !ok || r.Header.Get("User-Agent") != "web:unsolved-tests:v1 (by /u/test_operator)" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		authWriteJSON(w, map[string]any{"id": g.uid, "name": g.username})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeProviders) endpoints() auth.Endpoints {
	return auth.Endpoints{
		LinkedInIssuer: f.srv.URL + "/li",
		XAuthURL:       f.srv.URL + "/x/authorize",
		XTokenURL:      f.srv.URL + "/x/token",
		XUserURL:       f.srv.URL + "/x/users/me",
		XProfileBase:   "https://x.com/",
		GoogleIssuer:   f.srv.URL + "/google",
		GitHubAuthURL:  f.srv.URL + "/github/authorize",
		GitHubTokenURL: f.srv.URL + "/github/token",
		GitHubUserURL:  f.srv.URL + "/github/user",
		RedditAuthURL:  f.srv.URL + "/reddit/authorize",
		RedditTokenURL: f.srv.URL + "/reddit/token",
		RedditUserURL:  f.srv.URL + "/reddit/me",
	}
}

// redeem checks the code and its PKCE verifier (single use).
func (f *fakeProviders) redeem(r *http.Request) (fakeGrant, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.codes[r.PostFormValue("code")]
	delete(f.codes, r.PostFormValue("code"))
	if !ok || r.PostFormValue("grant_type") != "authorization_code" {
		return g, false
	}
	sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
	return g, authB64(sum[:]) == g.challenge
}

func (f *fakeProviders) idToken(g fakeGrant, provider string) string {
	key := f.key
	f.mu.Lock()
	if f.badSignature {
		key = f.wrongKey
	}
	f.mu.Unlock()
	now := time.Now()
	issuer, audience := f.srv.URL+"/"+map[string]string{"linkedin": "li", "google": "google"}[provider], map[string]string{"linkedin": "li-client", "google": "google-client"}[provider]
	if g.issuer != "" {
		issuer = g.issuer
	}
	if g.audience != "" {
		audience = g.audience
	}
	exp := now.Add(time.Hour)
	if g.expired {
		exp = now.Add(-time.Hour)
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": issuer, "sub": g.uid, "aud": audience,
		"iat": now.Unix(), "exp": exp.Unix(), "name": g.name,
	})
	input := authB64(header) + "." + authB64(claims)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return input + "." + authB64(sig)
}

func authB64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func authWriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

var fakeCodeSeq sync.Mutex
var fakeCodeN int

// signIn drives the whole browser flow for provider as uid and returns the
// callback response (not followed).
func (f *fakeProviders) signIn(c *testClient, provider, next string, g fakeGrant) *http.Response {
	c.t.Helper()
	start := c.get("/auth/" + provider + "/start?next=" + url.QueryEscape(next))
	start.Body.Close()
	if start.StatusCode != http.StatusFound {
		c.t.Fatalf("start: want 302, got %d", start.StatusCode)
	}
	loc, err := url.Parse(start.Header.Get("Location"))
	if err != nil {
		c.t.Fatal(err)
	}
	q := loc.Query()
	if !strings.HasPrefix(loc.String(), f.srv.URL+map[string]string{"linkedin": "/li/authorize", "x": "/x/authorize", "google": "/google/authorize", "github": "/github/authorize", "reddit": "/reddit/authorize"}[provider]) {
		c.t.Fatalf("start redirected to %s", loc)
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("state") == "" ||
		q.Get("redirect_uri") != c.app.Cfg.BaseURL+"/auth/"+provider+"/callback" {
		c.t.Fatalf("authorize URL missing PKCE/state/redirect: %s", loc)
	}
	wantScope := map[string]string{"linkedin": "openid profile", "x": "users.read tweet.read", "google": "openid profile", "github": "read:user", "reddit": "identity"}[provider]
	if q.Get("scope") != wantScope {
		c.t.Fatalf("scope %q, want %q", q.Get("scope"), wantScope)
	}
	fakeCodeSeq.Lock()
	fakeCodeN++
	code := fmt.Sprintf("code-%d", fakeCodeN)
	fakeCodeSeq.Unlock()
	g.challenge = q.Get("code_challenge")
	f.mu.Lock()
	f.codes[code] = g
	f.mu.Unlock()
	return c.get("/auth/" + provider + "/callback?code=" + code + "&state=" + url.QueryEscape(q.Get("state")))
}

func newAuthTestApp(t *testing.T) (*testApp, *fakeProviders) {
	t.Helper()
	app := newTestApp(t)
	f := newFakeProviders(t)
	app.H.Auth.UseEndpoints(f.endpoints())
	return app, f
}

func authHasSession(c *testClient) bool {
	su, _ := url.Parse(c.app.Srv.URL)
	for _, ck := range c.http.Jar.Cookies(su) {
		if ck.Name == auth.SessionCookie && ck.Value != "" {
			return true
		}
	}
	return false
}

func TestSignInFlows(t *testing.T) {
	app, f := newAuthTestApp(t)
	for _, provider := range []string{"linkedin", "x", "google", "github", "reddit"} {
		t.Run(provider, func(t *testing.T) {
			uid := fmt.Sprintf("%s-uid-%d", provider, time.Now().UnixNano())
			if provider == "github" {
				uid = fmt.Sprint(time.Now().UnixNano())
			}
			g := fakeGrant{uid: uid, name: "Ada Lovelace", username: "ada_l"}
			if provider == "github" {
				g.username = "ada-l"
			}

			// New user: signed in, sent to /welcome with next preserved.
			c := app.anon(t)
			resp := f.signIn(c, provider, "/p/some-problem", g)
			resp.Body.Close()
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("callback: %d %s", resp.StatusCode, body(t, resp))
			}
			if got := resp.Header.Get("Location"); got != "/welcome?next=%2Fp%2Fsome-problem" {
				t.Fatalf("new user redirected to %q", got)
			}
			if !authHasSession(c) {
				t.Fatal("no session cookie after sign-in")
			}
			page := c.get("/welcome?next=%2Fp%2Fsome-problem")
			if b := body(t, page); page.StatusCode != http.StatusOK || !strings.Contains(b, `name="handle"`) || !strings.Contains(b, `name="in_directory"`) {
				t.Fatalf("welcome page: %d", page.StatusCode)
			}
			handle := fmt.Sprintf("%s_%d", provider[:1], time.Now().UnixNano()%1e9)
			resp = c.postForm("/welcome", url.Values{"handle": {handle}, "in_directory": {"1"}, "next": {"/p/some-problem"}})
			resp.Body.Close()
			if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/p/some-problem" {
				t.Fatalf("welcome submit: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
			}
			p, err := app.Svc.ProfileByHandle(t.Context(), handle)
			if err != nil {
				t.Fatal(err)
			}
			wantName := "Ada Lovelace"
			if provider == "reddit" {
				wantName = "ada_l"
			}
			if p.DisplayName != wantName || !p.InDirectory {
				t.Fatalf("user after welcome: %+v", p)
			}
			// Stored identity: X gets a profile URL from the username; LinkedIn's OIDC claims have none.
			if provider == "x" && (len(p.Links) != 1 || p.Links[0].URL != "https://x.com/ada_l") {
				t.Fatalf("x profile link: %+v", p.Links)
			}
			if provider == "linkedin" && len(p.Links) != 0 {
				t.Fatalf("linkedin should store no profile URL: %+v", p.Links)
			}
			if provider == "google" && len(p.Links) != 0 {
				t.Fatalf("google should store no profile URL: %+v", p.Links)
			}
			if provider == "github" && (len(p.Links) != 1 || p.Links[0].URL != "https://github.com/ada-l") {
				t.Fatalf("github profile link: %+v", p.Links)
			}
			if provider == "reddit" && (len(p.Links) != 1 || p.Links[0].URL != "https://www.reddit.com/user/ada_l") {
				t.Fatalf("reddit profile link: %+v", p.Links)
			}

			// Returning user: straight to next, same account.
			c2 := app.anon(t)
			resp = f.signIn(c2, provider, "/problems", g)
			resp.Body.Close()
			if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/problems" {
				t.Fatalf("returning user: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
			}
			settings := c2.get("/settings")
			if b := body(t, settings); settings.StatusCode != http.StatusOK || !strings.Contains(b, handle) {
				t.Fatalf("returning user is not signed in as %s", handle)
			}
		})
	}
}

func TestSignInRejectsTampering(t *testing.T) {
	app, f := newAuthTestApp(t)
	g := fakeGrant{uid: "tamper-uid", name: "Mallory"}

	startState := func(c *testClient, provider string) (state string) {
		resp := c.get("/auth/" + provider + "/start?next=/")
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		state = loc.Query().Get("state")
		f.mu.Lock()
		f.codes["code-"+state] = fakeGrant{uid: g.uid, name: g.name, challenge: loc.Query().Get("code_challenge")}
		f.mu.Unlock()
		return state
	}
	stateCookie := func(c *testClient) *http.Cookie {
		su, _ := url.Parse(c.app.Srv.URL + "/auth/")
		for _, ck := range c.http.Jar.Cookies(su) {
			if ck.Name == auth.StateCookie {
				return ck
			}
		}
		t.Fatal("no state cookie")
		return nil
	}
	expectFail := func(t *testing.T, c *testClient, path string) {
		t.Helper()
		resp := c.get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d", path, resp.StatusCode)
		}
		if authHasSession(c) {
			t.Fatal("tampered sign-in created a session")
		}
	}

	t.Run("wrong state", func(t *testing.T) {
		c := app.anon(t)
		state := startState(c, "x")
		expectFail(t, c, "/auth/x/callback?code=code-"+state+"&state=not-the-state")
	})
	t.Run("no state cookie", func(t *testing.T) {
		c := app.anon(t)
		state := startState(c, "x")
		other := app.anon(t) // a different browser: login CSRF attempt
		expectFail(t, other, "/auth/x/callback?code=code-"+state+"&state="+url.QueryEscape(state))
	})
	t.Run("forged state cookie", func(t *testing.T) {
		c := app.anon(t)
		state := startState(c, "x")
		ck := stateCookie(c)
		payload, sig, _ := strings.Cut(ck.Value, ".")
		raw, _ := base64.RawURLEncoding.DecodeString(payload)
		raw = []byte(strings.Replace(string(raw), `"n":"/"`, `"n":"/evil"`, 1))
		su, _ := url.Parse(app.Srv.URL + "/auth/")
		c.http.Jar.SetCookies(su, []*http.Cookie{{Name: auth.StateCookie, Value: base64.RawURLEncoding.EncodeToString(raw) + "." + sig, Path: "/auth/"}})
		expectFail(t, c, "/auth/x/callback?code=code-"+state+"&state="+url.QueryEscape(state))
	})
	t.Run("state replayed", func(t *testing.T) {
		c := app.anon(t)
		state := startState(c, "x")
		ck := stateCookie(c)
		resp := c.get("/auth/x/callback?code=code-" + state + "&state=" + url.QueryEscape(state))
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("first use: %d", resp.StatusCode)
		}
		// The cookie was cleared; an attacker replaying it still can't reuse the code.
		c2 := app.anon(t)
		su, _ := url.Parse(app.Srv.URL + "/auth/")
		c2.http.Jar.SetCookies(su, []*http.Cookie{{Name: auth.StateCookie, Value: ck.Value, Path: "/auth/"}})
		expectFail(t, c2, "/auth/x/callback?code=code-"+state+"&state="+url.QueryEscape(state))
	})
	t.Run("provider mismatch", func(t *testing.T) {
		c := app.anon(t)
		state := startState(c, "x")
		expectFail(t, c, "/auth/linkedin/callback?code=code-"+state+"&state="+url.QueryEscape(state))
	})
	t.Run("wrong PKCE verifier", func(t *testing.T) {
		c := app.anon(t)
		state := startState(c, "x")
		f.mu.Lock()
		gg := f.codes["code-"+state]
		gg.challenge = "something-else"
		f.codes["code-"+state] = gg
		f.mu.Unlock()
		expectFail(t, c, "/auth/x/callback?code=code-"+state+"&state="+url.QueryEscape(state))
	})
	t.Run("provider error", func(t *testing.T) {
		c := app.anon(t)
		state := startState(c, "x")
		expectFail(t, c, "/auth/x/callback?error=access_denied&state="+url.QueryEscape(state))
	})
	t.Run("bad id token signature", func(t *testing.T) {
		f.mu.Lock()
		f.badSignature = true
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.badSignature = false; f.mu.Unlock() }()
		c := app.anon(t)
		state := startState(c, "linkedin")
		expectFail(t, c, "/auth/linkedin/callback?code=code-"+state+"&state="+url.QueryEscape(state))
	})
	t.Run("google bad id token signature", func(t *testing.T) {
		f.mu.Lock()
		f.badSignature = true
		f.mu.Unlock()
		defer func() { f.mu.Lock(); f.badSignature = false; f.mu.Unlock() }()
		c := app.anon(t)
		resp := f.signIn(c, "google", "/", fakeGrant{uid: "bad-google-signature", name: "Mallory"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || authHasSession(c) {
			t.Fatalf("forged Google token: status %d", resp.StatusCode)
		}
	})
	for _, tc := range []struct {
		name  string
		grant fakeGrant
	}{
		{"issuer", fakeGrant{uid: "google-issuer", name: "Mallory", issuer: "https://wrong.example"}},
		{"audience", fakeGrant{uid: "google-audience", name: "Mallory", audience: "wrong-client"}},
		{"expiry", fakeGrant{uid: "google-expiry", name: "Mallory", expired: true}},
	} {
		t.Run("google wrong "+tc.name, func(t *testing.T) {
			c := app.anon(t)
			resp := f.signIn(c, "google", "/", tc.grant)
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest || authHasSession(c) {
				t.Fatalf("invalid Google token: status %d, session %v", resp.StatusCode, authHasSession(c))
			}
		})
	}
	t.Run("github malformed identity", func(t *testing.T) {
		c := app.anon(t)
		resp := f.signIn(c, "github", "/", fakeGrant{uid: "not-an-id", name: "Mallory", username: "mal"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || authHasSession(c) {
			t.Fatalf("malformed GitHub identity: status %d", resp.StatusCode)
		}
	})
	t.Run("github negative id", func(t *testing.T) {
		c := app.anon(t)
		resp := f.signIn(c, "github", "/", fakeGrant{uid: "-1", name: "Mallory", username: "mal"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || authHasSession(c) {
			t.Fatalf("negative GitHub ID: status %d", resp.StatusCode)
		}
	})
	t.Run("reddit empty identity", func(t *testing.T) {
		c := app.anon(t)
		resp := f.signIn(c, "reddit", "/", fakeGrant{uid: "", name: "Mallory", username: "mal"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest || authHasSession(c) {
			t.Fatalf("empty Reddit identity: status %d", resp.StatusCode)
		}
	})
	t.Run("open redirect in next", func(t *testing.T) {
		for _, next := range []string{"//evil.example", "https://evil.example", `/\evil.example`, "evil"} {
			c := app.anon(t)
			resp := f.signIn(c, "x", next, fakeGrant{uid: fmt.Sprintf("redir-%d", time.Now().UnixNano()), name: "R"})
			resp.Body.Close()
			if got := resp.Header.Get("Location"); got != "/welcome?next=%2F" {
				t.Fatalf("next %q → %q", next, got)
			}
		}
	})
	t.Run("unknown provider", func(t *testing.T) {
		c := app.anon(t)
		resp := c.get("/auth/unknown/start")
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("want 404, got %d", resp.StatusCode)
		}
	})
}

func TestSameNameAndUIDAcrossProvidersStayDistinct(t *testing.T) {
	app, f := newAuthTestApp(t)
	for _, provider := range []string{"github", "reddit"} {
		c := app.anon(t)
		resp := f.signIn(c, provider, "/", fakeGrant{uid: "123456789", name: "Same Name", username: "same_name"})
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || !authHasSession(c) {
			t.Fatalf("%s: status %d", provider, resp.StatusCode)
		}
	}
	github, err := app.Store.GetIdentity(t.Context(), store.GetIdentityParams{Provider: store.ProviderGithub, ProviderUid: "123456789"})
	if err != nil {
		t.Fatal(err)
	}
	reddit, err := app.Store.GetIdentity(t.Context(), store.GetIdentityParams{Provider: store.ProviderReddit, ProviderUid: "123456789"})
	if err != nil {
		t.Fatal(err)
	}
	if github.UserID == reddit.UserID {
		t.Fatal("matching provider uid and name merged separate accounts")
	}
}

func TestSignInRateLimit(t *testing.T) {
	app, _ := newAuthTestApp(t)
	c := app.anon(t)
	for i := 1; i <= 21; i++ {
		resp := c.get("/auth/x/start")
		resp.Body.Close()
		want := http.StatusFound
		if i == 21 {
			want = http.StatusTooManyRequests
		}
		if resp.StatusCode != want {
			t.Fatalf("start #%d: want %d, got %d", i, want, resp.StatusCode)
		}
	}
	// Keyed by IP, not by session: signing in doesn't reset it.
	u := app.newUser(t)
	resp := app.as(t, u).get("/auth/linkedin/start")
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("signed-in start from same IP: want 429, got %d", resp.StatusCode)
	}
}

func TestSignInPage(t *testing.T) {
	app := newTestApp(t)
	resp := app.anon(t).get("/signin?next=%2Fp%2Fabc")
	b := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(b, "/auth/linkedin/start?next=%2Fp%2Fabc") || !strings.Contains(b, "/auth/x/start?next=%2Fp%2Fabc") {
		t.Fatalf("signin page: %d\n%s", resp.StatusCode, b)
	}
	resp = app.anon(t).get("/signin?next=%2F%2Fevil.example")
	if b := body(t, resp); strings.Contains(b, "evil.example") {
		t.Fatal("unsafe next rendered into sign-in links")
	}
	// Signed-in visitors skip the chooser.
	resp = app.as(t, app.newUser(t)).get("/signin?next=%2Fproblems")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/problems" {
		t.Fatalf("signed-in /signin: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestUnconfiguredSignIn(t *testing.T) {
	app := newTestApp(t, func(c *config.Config) {
		c.LinkedInClientID, c.LinkedInClientSecret = "replace-me", "replace-me"
		c.XClientID, c.XClientSecret = "", ""
		c.GoogleClientID, c.GoogleClientSecret = "", ""
		c.GitHubClientID, c.GitHubClientSecret = "", ""
		c.RedditClientID, c.RedditClientSecret = "", ""
	})
	c := app.anon(t)
	resp := c.get("/signin?next=/new")
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(got, "Sign-in is not available yet") {
		t.Fatal("unconfigured sign-in must explain availability")
	}
	if strings.Contains(got, "/auth/linkedin/start") || strings.Contains(got, "/auth/x/start") || strings.Contains(got, "/auth/google/start") || strings.Contains(got, "/auth/github/start") || strings.Contains(got, "/auth/reddit/start") {
		t.Fatal("unconfigured providers must not offer sign-in links")
	}
	for _, provider := range []string{"linkedin", "x", "google", "github", "reddit"} {
		resp = c.get("/auth/" + provider + "/start")
		got = body(t, resp)
		if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Location") != "" || !strings.Contains(got, "Sign-in is not available yet") {
			t.Errorf("unconfigured %s must stay local and explain availability; status %d", provider, resp.StatusCode)
		}
		for _, cookie := range resp.Cookies() {
			if cookie.Name == auth.StateCookie {
				t.Error("unconfigured provider must not initiate OAuth state")
			}
		}
	}
}

func TestSignInShowsOnlyConfiguredProviders(t *testing.T) {
	app := newTestApp(t, func(c *config.Config) {
		c.LinkedInClientSecret = "replace-me"
		c.GoogleClientSecret = "replace-me"
		c.GitHubClientSecret = ""
		c.RedditClientID = "replace-me"
	})
	resp := app.anon(t).get("/signin?next=%2Fnew")
	got := body(t, resp)
	if strings.Contains(got, "/auth/linkedin/start") || !strings.Contains(got, "/auth/x/start?next=%2Fnew") || strings.Contains(got, "/auth/google/start") || strings.Contains(got, "/auth/github/start") || strings.Contains(got, "/auth/reddit/start") {
		t.Fatal("only configured providers should be offered, with the return path preserved")
	}
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "/",
		"/":                 "/",
		"/p/123?x=1":        "/p/123?x=1",
		"//evil.com":        "/",
		"/\\evil.com":       "/",
		"https://evil.com":  "/",
		"evil.com":          "/",
		"/ok\r\nSet-Cookie": "/",
	} {
		if got := auth.SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}
