package http

import (
	"net/http"
	"strings"
	"testing"
)

func TestPrivacyAndAboutRender(t *testing.T) {
	app := newTestApp(t)
	c := app.anon(t)
	for path, want := range map[string][]string{
		"/privacy": {"profile URL", "hidden from other users", "/settings/export", "deleted user"},
		"/about":   {"30-day half-life", "kept private", "soft-solved"},
	} {
		resp := c.get(path)
		b := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %d", path, resp.StatusCode)
		}
		for _, w := range want {
			if !strings.Contains(b, w) {
				t.Errorf("%s missing %q", path, w)
			}
		}
	}
}
