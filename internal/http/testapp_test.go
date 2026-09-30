package http

// Shared end-to-end test harness. Every phase's e2e test builds a testApp:
// the real router, services, auth and River workers over a real Postgres
// (testcontainers, one per package). Add new helpers in your own _test.go file.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

type testApp struct {
	Srv   *httptest.Server
	Cfg   config.Config
	Store *store.Store
	Svc   *service.Service
	River *river.Client[pgx.Tx]
	H     *Handlers
}

// testConfig is the config every e2e test runs with. Tweak a copy per test.
func testConfig(dbURL string) config.Config {
	return config.Config{
		DatabaseURL:            dbURL,
		BaseURL:                "http://unsolved.test",
		SessionTTL:             720 * time.Hour,
		LinkedInClientID:       "li-client",
		LinkedInClientSecret:   "li-secret",
		XClientID:              "x-client",
		XClientSecret:          "x-secret",
		RankHalfLife:           720 * time.Hour,
		RankMinVotesToTakeOver: 3,
		SoftSolvedThreshold:    5,
		AdminHandles:           []string{"admin_user"},
	}
}

// newTestApp starts the full app. Pass a func to adjust the config.
func newTestApp(t *testing.T, tweak ...func(*config.Config)) *testApp {
	t.Helper()
	pool := storetest.Pool(t)
	cfg := testConfig(storetest.URL(t))
	for _, f := range tweak {
		f(&cfg)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	st := store.Open(pool)
	scorer := ranking.NewDecayScorer(cfg.RankHalfLife)
	rc := jobs.Config(jobs.Deps{Store: st, Scorer: scorer, Cfg: cfg, Log: log})
	rc.PeriodicJobs = nil
	rc.FetchCooldown = 50 * time.Millisecond
	rc.FetchPollInterval = 100 * time.Millisecond
	riverClient, err := river.NewClient(riverpgxv5.New(pool), rc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := riverClient.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = riverClient.Stop(sctx)
	})
	svc := service.New(st, riverClient, scorer, service.NewWeigher(st, cfg), cfg, log)
	a := auth.New(cfg, svc, log)
	h := NewHandlers(svc, a, cfg, log)
	srv := httptest.NewServer(h.Router("../../static"))
	t.Cleanup(srv.Close)
	return &testApp{Srv: srv, Cfg: cfg, Store: st, Svc: svc, River: riverClient, H: h}
}

var userSeq atomic.Int64

// newUser creates a signed-up user through the same service call the OAuth
// callback uses. The handle is a generated placeholder (see u.Handle).
func (a *testApp) newUser(t *testing.T) store.User {
	t.Helper()
	n := userSeq.Add(1)
	u, created, err := a.Svc.SignInWithIdentity(context.Background(), store.ProviderX,
		fmt.Sprintf("uid-%d-%d", time.Now().UnixNano(), n), "https://x.com/someone", fmt.Sprintf("Test User %d", n))
	if err != nil || !created {
		t.Fatalf("newUser: created=%v err=%v", created, err)
	}
	return u
}

// testClient is a browser: cookie jar, CSRF token, no redirect following.
type testClient struct {
	t    *testing.T
	app  *testApp
	http *http.Client
	csrf string
}

// anon returns a signed-out client.
func (a *testApp) anon(t *testing.T) *testClient {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &testClient{t: t, app: a, http: &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
	c.refreshCSRF()
	return c
}

// as returns a client signed in as u (a real session row + cookie).
func (a *testApp) as(t *testing.T, u store.User) *testClient {
	t.Helper()
	c := a.anon(t)
	token, _, err := a.Svc.NewSession(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	su, _ := url.Parse(a.Srv.URL)
	c.http.Jar.SetCookies(su, []*http.Cookie{{Name: auth.SessionCookie, Value: token, Path: "/"}})
	return c
}

func (c *testClient) refreshCSRF() {
	resp := c.get("/healthz") // every response sets the CSRF cookie
	resp.Body.Close()
	su, _ := url.Parse(c.app.Srv.URL)
	for _, ck := range c.http.Jar.Cookies(su) {
		if ck.Name == auth.CSRFCookie {
			c.csrf = ck.Value
		}
	}
	if c.csrf == "" {
		c.t.Fatal("no CSRF cookie set")
	}
}

func (c *testClient) do(req *http.Request) *http.Response {
	c.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func (c *testClient) get(path string) *http.Response {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.app.Srv.URL+path, nil)
	return c.do(req)
}

// getHX issues a GET with the HX-Request header set.
func (c *testClient) getHX(path string) *http.Response {
	c.t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.app.Srv.URL+path, nil)
	req.Header.Set("HX-Request", "true")
	return c.do(req)
}

// postForm posts a form with the CSRF field filled in.
func (c *testClient) postForm(path string, form url.Values) *http.Response {
	c.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set(auth.CSRFFormField, c.csrf)
	req, _ := http.NewRequest(http.MethodPost, c.app.Srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// postHX posts like htmx does: CSRF in the header, HX-Request set.
func (c *testClient) postHX(path string, form url.Values) *http.Response {
	c.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	req, _ := http.NewRequest(http.MethodPost, c.app.Srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set(auth.CSRFHeader, c.csrf)
	return c.do(req)
}

// body reads and closes the response body.
func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// eventually polls cond until it is true or timeout passes.
func eventually(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not met within", timeout)
}
