package http

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hi-naresh/unsolved/internal/config"
)

func TestRefinementDiscoverableAndProtected(t *testing.T) {
	app := newTestApp(t, func(c *config.Config) { c.RankMinVotesToTakeOver = 7 })
	author := app.newUser(t)
	pid := postProblem(t, app.as(t, author), problemForm("Refine the weekly order entry process"))
	visitor := app.anon(t)
	page := mustStatus(t, visitor.get("/p/"+pid), http.StatusOK)
	for _, text := range []string{"Refine or correct", "Revision history", `href="/p/` + pid + `/revise"`} {
		if !strings.Contains(page, text) {
			t.Fatalf("problem page missing %q", text)
		}
	}
	resp := visitor.get("/p/" + pid + "/revise")
	mustStatus(t, resp, http.StatusSeeOther)
	if resp.Header.Get("Location") != "/signin?next="+url.QueryEscape("/p/"+pid+"/revise") {
		t.Fatalf("lost correction return path: %s", resp.Header.Get("Location"))
	}
	page = mustStatus(t, visitor.get("/p/"+pid+"/evolution"), http.StatusOK)
	if !strings.Contains(page, "at least 7 votes") {
		t.Fatal("history must explain configured threshold")
	}
	page = mustStatus(t, app.as(t, author).get("/p/"+pid+"/revise"), http.StatusOK)
	if !strings.Contains(page, "at least 7 votes") {
		t.Fatal("form must explain configured threshold")
	}
	execSQL(t, app, `UPDATE users SET suspended_at = now() WHERE id = $1`, author.ID)
	page = mustStatus(t, app.as(t, author).get("/p/"+pid), http.StatusOK)
	if strings.Contains(page, `href="/p/`+pid+`/revise"`) {
		t.Fatal("suspended user offered correction")
	}
	execSQL(t, app, `UPDATE problems SET state = 'invalid' WHERE id = $1`, pid)
	page = mustStatus(t, visitor.get("/p/"+pid), http.StatusOK)
	if strings.Contains(page, `href="/p/`+pid+`/revise"`) {
		t.Fatal("invalid problem offered correction")
	}
}
