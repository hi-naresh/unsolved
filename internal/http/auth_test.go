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
			"id_token": f.idToken(g),
		})
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

func (f *fakeProviders) idToken(g fakeGrant) string {
	key := f.key
	f.mu.Lock()
	if f.badSignature {
		key = f.wrongKey
	}
	f.mu.Unlock()
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": f.srv.URL + "/li", "sub": g.uid, "aud": "li-client",
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "name": g.name,
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
	if !strings.HasPrefix(loc.String(), f.srv.URL+map[string]string{"linkedin": "/li/authorize", "x": "/x/authorize"}[provider]) {
		c.t.Fatalf("start redirected to %s", loc)
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("state") == "" ||
		q.Get("redirect_uri") != c.app.Cfg.BaseURL+"/auth/"+provider+"/callback" {
		c.t.Fatalf("authorize URL missing PKCE/state/redirect: %s", loc)
	}
	wantScope := map[string]string{"linkedin": "openid profile", "x": "users.read tweet.read"}[provider]
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
	for _, provider := range []string{"linkedin", "x"} {
		t.Run(provider, func(t *testing.T) {
			uid := fmt.Sprintf("%s-uid-%d", provider, time.Now().UnixNano())
			g := fakeGrant{uid: uid, name: "Ada Lovelace", username: "ada_l"}

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
			if p.DisplayName != "Ada Lovelace" || !p.InDirectory {
				t.Fatalf("user after welcome: %+v", p)
			}
			// Stored identity: X gets a profile URL from the username; LinkedIn's OIDC claims have none.
			if provider == "x" && (len(p.Links) != 1 || p.Links[0].URL != "https://x.com/ada_l") {
				t.Fatalf("x profile link: %+v", p.Links)
			}
			if provider == "linkedin" && len(p.Links) != 0 {
				t.Fatalf("linkedin should store no profile URL: %+v", p.Links)
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
		resp := c.get("/auth/github/start")
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("want 404, got %d", resp.StatusCode)
		}
	})
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
