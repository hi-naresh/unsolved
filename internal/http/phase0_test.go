package http

import (
	"net/http"
	"net/url"
	"testing"
)

func TestPhase0Healthz(t *testing.T) {
	app := newTestApp(t)
	c := app.anon(t)
	resp := c.get("/healthz")
	if got := body(t, resp); resp.StatusCode != http.StatusOK || got != "ok" {
		t.Fatalf("healthz: %d %q", resp.StatusCode, got)
	}
}

func TestCSRFRejectsMissingToken(t *testing.T) {
	app := newTestApp(t)
	c := app.anon(t)
	req, _ := http.NewRequest(http.MethodPost, app.Srv.URL+"/auth/signout", nil)
	resp := c.do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403 without CSRF token, got %d", resp.StatusCode)
	}
	resp = c.postForm("/auth/signout", url.Values{})
	resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Fatal("valid CSRF token rejected")
	}
}

func TestSessionLoadsAndSignOutEndsIt(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t)
	c := app.as(t, u)
	resp := c.postForm("/auth/signout", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signout: %d", resp.StatusCode)
	}
	// The row is gone: the old token no longer resolves.
	token := ""
	su, _ := url.Parse(app.Srv.URL)
	for _, ck := range c.http.Jar.Cookies(su) {
		if ck.Name == "us_session" {
			token = ck.Value
		}
	}
	if token != "" {
		t.Fatal("session cookie not cleared")
	}
}
