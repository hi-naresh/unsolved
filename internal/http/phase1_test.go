package http

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
)

// Phase 1 acceptance: guided form, revisions, voting and current-version
// takeover, evolution tree, fork, anonymity, front-page keyset pagination,
// rate limits, suspended users.
func TestPhase1PostReviseVote(t *testing.T) {
	app := newTestApp(t)
	author, editor := app.newUser(t), app.newUser(t)
	ac, ec := app.as(t, author), app.as(t, editor)

	// The guided form, fields in the doc's order, with the duplicate-check hook.
	b := mustStatus(t, ac.get("/new"), http.StatusOK)
	last := -1
	for _, f := range []string{`name="domain_id"`, `name="current_process"`, `name="pain"`, `name="tried"`, `name="title"`, `name="display"`} {
		i := strings.Index(b, f)
		if i < 0 || i < last {
			t.Fatalf("form field %s missing or out of order", f)
		}
		last = i
	}
	for _, want := range []string{`id="similar"`, `hx-post="/new/similar"`, "Posting as @" + author.Handle, "Posting anonymously"} {
		if !strings.Contains(b, want) {
			t.Fatalf("form lacks %q", want)
		}
	}
	resp := app.anon(t).get("/new")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/signin") {
		t.Fatalf("signed-out /new: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Validation errors re-render the form with the input, 422.
	bad := problemForm("short")
	b = mustStatus(t, ac.postForm("/problems", bad), http.StatusUnprocessableEntity)
	if !strings.Contains(b, "The title needs at least 10 characters") || !strings.Contains(b, "re-key them into the ERP") {
		t.Fatalf("422 form lacks message or input: %s", b)
	}

	// Post → redirect → the page shows it; posting-as choice remembered.
	pid := postProblem(t, ac, problemForm("Re-keying weekly orders into the ERP"))
	su, _ := url.Parse(app.Srv.URL)
	var display string
	for _, ck := range ac.http.Jar.Cookies(su) {
		if ck.Name == "us_display" {
			display = ck.Value
		}
	}
	if display != "named" {
		t.Fatalf("us_display cookie = %q", display)
	}
	b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, "Re-keying weekly orders into the ERP") || !strings.Contains(b, "@"+author.Handle) {
		t.Fatal("problem page lacks title or author")
	}
	root := currentRevision(t, app, pid)

	// Revise: prefilled form; without a why-note → 422; with one → evolution.
	b = mustStatus(t, ec.get("/p/"+pid+"/revise"), http.StatusOK)
	if !strings.Contains(b, `value="`+root+`"`) || !strings.Contains(b, "Re-keying weekly orders into the ERP") {
		t.Fatal("revise form not prefilled from the current revision")
	}
	rev := problemForm("Re-keying weekly orders into the ERP by hand")
	rev.Set("parent_revision_id", root)
	b = mustStatus(t, ec.postForm("/p/"+pid+"/revisions", rev), http.StatusUnprocessableEntity)
	if !strings.Contains(b, `id="why_note-error"`) {
		t.Fatal("missing why-note not reported")
	}
	rev.Set("why_note", "Said that it is done by hand.")
	resp = ec.postForm("/p/"+pid+"/revisions", rev)
	mustStatus(t, resp, http.StatusSeeOther)
	if !strings.HasPrefix(resp.Header.Get("Location"), "/p/"+pid+"/evolution") {
		t.Fatalf("revision redirect: %s", resp.Header.Get("Location"))
	}
	var child string
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT id FROM problem_revisions WHERE parent_revision_id = $1`, root).Scan(&child); err != nil {
		t.Fatal(err)
	}

	// Self-vote forbidden.
	mustStatus(t, ec.postHX("/r/"+child+"/vote", nil), http.StatusForbidden)

	// Voting: htmx partial with the truth; toggling off and on again.
	v1 := app.as(t, app.newUser(t))
	b = mustStatus(t, v1.postHX("/r/"+child+"/vote", nil), http.StatusOK)
	if !strings.Contains(b, `aria-pressed="true"`) || !strings.Contains(b, `<span class="vote-count">1</span>`) || strings.Contains(b, "<html") {
		t.Fatalf("vote partial: %s", b)
	}
	b = mustStatus(t, v1.postHX("/r/"+child+"/vote", nil), http.StatusOK)
	if !strings.Contains(b, `aria-pressed="false"`) || !strings.Contains(b, `<span class="vote-count">0</span>`) {
		t.Fatalf("unvote partial: %s", b)
	}
	// Without JS: a form POST redirects back to the problem.
	resp = v1.postForm("/r/"+child+"/vote", nil)
	mustStatus(t, resp, http.StatusSeeOther)
	if resp.Header.Get("Location") != "/p/"+pid {
		t.Fatalf("no-JS vote redirect: %s", resp.Header.Get("Location"))
	}
	// Two more voters → 3 votes → the revision takes over within 10 s.
	for range 2 {
		mustStatus(t, app.as(t, app.newUser(t)).postHX("/r/"+child+"/vote", nil), http.StatusOK)
	}
	eventually(t, 10*time.Second, func() bool {
		return strings.Contains(body(t, app.anon(t).get("/p/"+pid)), "<h1 class=\"text-2xl font-semibold leading-tight\">Re-keying weekly orders into the ERP by hand</h1>")
	})
	if currentRevision(t, app, pid) != child {
		t.Fatal("current_revision_id not moved")
	}

	// Evolution tree: both versions, the why-note, the current badge.
	b = mustStatus(t, app.anon(t).get("/p/"+pid+"/evolution"), http.StatusOK)
	for _, want := range []string{"Said that it is done by hand.", "First version", `id="r-` + root, `id="r-` + child, ">current</span>"} {
		if !strings.Contains(b, want) {
			t.Fatalf("evolution page lacks %q", want)
		}
	}
	if strings.Index(b, `id="r-`+root) > strings.Index(b, `id="r-`+child) {
		t.Fatal("child listed before its parent")
	}

	// Fork: new problem linked both ways; votes not copied.
	resp = ec.postForm("/p/"+pid+"/fork", url.Values{"revision_id": {root}})
	mustStatus(t, resp, http.StatusSeeOther)
	fork := strings.TrimPrefix(resp.Header.Get("Location"), "/p/")
	if _, err := uuid.Parse(fork); err != nil || fork == pid {
		t.Fatalf("fork redirect %q", resp.Header.Get("Location"))
	}
	b = mustStatus(t, app.anon(t).get("/p/"+fork), http.StatusOK)
	if !strings.Contains(b, "Forked from") || !strings.Contains(b, `href="/p/`+pid+`"`) || !strings.Contains(b, `<span class="vote-count">0</span>`) {
		t.Fatal("fork page does not link back or copied votes")
	}
	b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, "Forks:") || !strings.Contains(b, `href="/p/`+fork+`"`) {
		t.Fatal("original does not link to its fork")
	}
}

func TestPhase1Anonymity(t *testing.T) {
	app := newTestApp(t)
	anonAuthor, anonEditor := app.newUser(t), app.newUser(t)
	c := app.as(t, anonAuthor)
	form := problemForm("Anonymous: approving expenses over email")
	form.Set("display", "anonymous")
	pid := postProblem(t, c, form)

	rev := problemForm("Anonymous: approving expenses over email chains")
	rev.Set("display", "anonymous")
	rev.Set("parent_revision_id", currentRevision(t, app, pid))
	rev.Set("why_note", "Clarified.")
	mustStatus(t, app.as(t, anonEditor).postForm("/p/"+pid+"/revisions", rev), http.StatusSeeOther)

	viewer := app.anon(t)
	pages := map[string]string{
		"problem":   mustStatus(t, viewer.get("/p/"+pid), http.StatusOK),
		"evolution": mustStatus(t, viewer.get("/p/"+pid+"/evolution"), http.StatusOK),
		"new list":  mustStatus(t, viewer.get("/problems?sort=new"), http.StatusOK),
		"domain":    mustStatus(t, viewer.get("/problems?domain=manufacturing&sort=new"), http.StatusOK),
	}
	for name, b := range pages {
		if !strings.Contains(b, "Anonymous") {
			t.Fatalf("%s: no Anonymous label", name)
		}
		for _, h := range []string{anonAuthor.Handle, anonEditor.Handle} {
			if strings.Contains(b, h) {
				t.Fatalf("%s page leaks handle %s", name, h)
			}
		}
	}
	// The front page, wherever the problem lands on it.
	ids, pagesHTML := walkListHTML(t, viewer, "/")
	if countOf(ids, pid) != 1 {
		t.Fatal("anonymous problem not on the front page")
	}
	if strings.Contains(pagesHTML, anonAuthor.Handle) || strings.Contains(pagesHTML, anonEditor.Handle) {
		t.Fatal("front page leaks handle")
	}
}

func TestPhase1FrontPagePagination(t *testing.T) {
	app := newTestApp(t)
	author := app.newUser(t)
	var ids []string
	for i := range 35 {
		ids = append(ids, problemDirect(t, app, author.ID, fmt.Sprintf("Paging problem %02d: delivery status calls", i), store.DisplayModeNamed).String())
	}
	c := app.anon(t)
	for _, path := range []string{"/", "/problems", "/problems?sort=new", "/problems?domain=logistics&state=open"} {
		got := walkList(t, c, path)
		for _, id := range ids {
			if n := countOf(got, id); n != 1 {
				t.Fatalf("%s: problem %s listed %d times", path, id, n)
			}
		}
	}
	// sort=new lists newest first.
	got := walkList(t, c, "/problems?sort=new")
	if a, b := indexOf(got, ids[34]), indexOf(got, ids[0]); a > b {
		t.Fatal("sort=new not newest first")
	}
	mustStatus(t, c.get("/?after=nonsense"), http.StatusUnprocessableEntity)
}

func indexOf(ids []string, id string) int {
	for i, x := range ids {
		if x == id {
			return i
		}
	}
	return -1
}

func TestPhase1RateLimitAndSuspension(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t)
	c := app.as(t, u)
	for i := range 3 {
		postProblem(t, c, problemForm(fmt.Sprintf("Rate limited problem number %d", i)))
	}
	resp := c.postForm("/problems", problemForm("Rate limited problem number 4"))
	mustStatus(t, resp, http.StatusTooManyRequests)

	// Suspended users cannot write anything.
	s := app.newUser(t)
	other := app.newUser(t)
	pid := problemDirect(t, app, other.ID, "Suspension target: counting pallets", store.DisplayModeNamed).String()
	rid := currentRevision(t, app, pid)
	sc := app.as(t, s)
	execSQL(t, app, `UPDATE users SET suspended_at = now() WHERE id = $1`, s.ID)
	mustStatus(t, sc.postForm("/problems", problemForm("Suspended user tries to post")), http.StatusForbidden)
	mustStatus(t, sc.postHX("/r/"+rid+"/vote", nil), http.StatusForbidden)
	rev := problemForm("Suspended user tries to revise")
	rev.Set("parent_revision_id", rid)
	rev.Set("why_note", "x")
	mustStatus(t, sc.postForm("/p/"+pid+"/revisions", rev), http.StatusForbidden)
	mustStatus(t, sc.postForm("/p/"+pid+"/fork", url.Values{"revision_id": {rid}}), http.StatusForbidden)
	// Reading still works.
	mustStatus(t, sc.get("/p/"+pid), http.StatusOK)
	// Signed out: writes are refused.
	resp = app.anon(t).postForm("/r/"+rid+"/vote", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed-out vote: %d", resp.StatusCode)
	}
}
