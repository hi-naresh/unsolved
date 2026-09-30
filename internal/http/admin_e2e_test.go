package http

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/store"
)

// modAdmin creates a user and gives it the handle listed in the test
// config's ADMIN_HANDLES ("admin_user"), freeing the handle first if an
// earlier test in this package already used it.
func (a *testApp) modAdmin(t *testing.T) store.User {
	t.Helper()
	u := a.newUser(t)
	a.modExec(t, `UPDATE users SET handle = 'x' || substr(md5(id::text), 1, 12) WHERE handle = 'admin_user'`)
	a.modExec(t, `UPDATE users SET handle = 'admin_user' WHERE id = $1`, u.ID)
	u.Handle = "admin_user"
	return u
}

func (a *testApp) modExec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := a.Store.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// modProblem inserts a problem with one revision by author; returns (problem id, revision id).
func (a *testApp) modProblem(t *testing.T, author uuid.UUID, display string, title string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	pid, _ := uuid.NewV7()
	rid, _ := uuid.NewV7()
	a.modExec(t, `INSERT INTO problems (id, domain_id, author_id, author_display) VALUES ($1, 3, $2, $3)`, pid, author, display)
	a.modExec(t, `INSERT INTO problem_revisions (id, problem_id, author_id, author_display, title, current_process, pain, tried)
		VALUES ($1, $2, $3, $4, $5, $6, 'Costs a day every week to do by hand.', 'nothing yet')`,
		rid, pid, author, display, title, strings.Repeat("We copy the numbers from one sheet to another. ", 3))
	a.modExec(t, `UPDATE problems SET current_revision_id = $2 WHERE id = $1`, pid, rid)
	return pid, rid
}

func (a *testApp) modScalar(t *testing.T, sql string, args ...any) any {
	t.Helper()
	var v any
	if err := a.Store.Pool.QueryRow(context.Background(), sql, args...).Scan(&v); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return v
}

func TestAdminHiddenFromNonAdmins(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t)
	c := app.as(t, u)
	for _, path := range []string{"/admin", "/admin/"} {
		resp := c.get(path)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s as non-admin: %d, want 404", path, resp.StatusCode)
		}
	}
	resp := c.postForm("/admin/rescore", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("POST /admin/rescore as non-admin: %d, want 404", resp.StatusCode)
	}
	resp = c.postForm("/admin/users/"+u.Handle+"/suspend", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("suspend as non-admin: %d, want 404", resp.StatusCode)
	}
	resp = app.anon(t).get("/admin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /admin signed out: %d, want redirect to sign in", resp.StatusCode)
	}
}

func TestReportsAndAdminModeration(t *testing.T) {
	app := newTestApp(t)
	app.modExec(t, `UPDATE reports SET resolved_at = now() WHERE resolved_at IS NULL`)
	admin := app.modAdmin(t)
	reporter := app.newUser(t)
	author := app.newUser(t)
	troll := app.newUser(t)
	pid, rid := app.modProblem(t, author.ID, "anonymous", "Invoices are keyed in twice by hand")

	rc := app.as(t, reporter)

	// htmx report on an anonymous problem revision → thanks partial.
	resp := rc.postHX("/report", url.Values{"target_kind": {"problem_revision"}, "target_id": {rid.String()}, "reason": {"This is an advert for a product"}})
	if b := body(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(b, "an admin will look at this") {
		t.Fatalf("htmx report: %d %q", resp.StatusCode, b)
	}
	// Plain report on a user → redirect back to the same-origin Referer.
	form := url.Values{"target_kind": {"user"}, "target_id": {troll.ID.String()}, "reason": {"Abusive replies everywhere"}, auth.CSRFFormField: {rc.csrf}}
	req, _ := http.NewRequest(http.MethodPost, app.Srv.URL+"/report", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", app.Srv.URL+"/u/"+troll.Handle+"?x=1")
	resp = rc.do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/u/"+troll.Handle+"?x=1" {
		t.Fatalf("plain report: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Foreign Referer → "/".
	req, _ = http.NewRequest(http.MethodPost, app.Srv.URL+"/report", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://evil.example/phish")
	resp = rc.do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("foreign referer: %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// Validation and missing target.
	resp = rc.postHX("/report", url.Values{"target_kind": {"user"}, "target_id": {troll.ID.String()}, "reason": {"no"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("short reason: %d", resp.StatusCode)
	}
	resp = rc.postHX("/report", url.Values{"target_kind": {"user"}, "target_id": {uuid.NewString()}, "reason": {"Nobody at all here"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing target: %d", resp.StatusCode)
	}
	// Signed out → must sign in.
	resp = app.anon(t).postForm("/report", url.Values{"target_kind": {"user"}, "target_id": {troll.ID.String()}, "reason": {"Abusive replies everywhere"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed-out report: %d", resp.StatusCode)
	}

	// The admin sees the reports; the anonymous author's handle never appears.
	ac := app.as(t, admin)
	resp = ac.get("/admin")
	page := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/admin: %d", resp.StatusCode)
	}
	for _, want := range []string{"This is an advert for a product", "Abusive replies everywhere", "@" + reporter.Handle, "@" + troll.Handle, "Anonymous", "Invoices are keyed in twice by hand", "/p/" + pid.String()} {
		if !strings.Contains(page, want) {
			t.Errorf("/admin missing %q", want)
		}
	}
	if strings.Contains(page, author.Handle) {
		t.Error("/admin shows the anonymous author's handle")
	}

	// Suspend: the troll's next write is 403; unsuspend restores it.
	tc := app.as(t, troll)
	resp = ac.postForm("/admin/users/"+troll.Handle+"/suspend", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("suspend: %d", resp.StatusCode)
	}
	resp = tc.postHX("/report", url.Values{"target_kind": {"user"}, "target_id": {reporter.ID.String()}, "reason": {"Retaliation report"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("suspended user's write: %d, want 403", resp.StatusCode)
	}
	resp = ac.postForm("/admin/users/"+troll.Handle+"/unsuspend", nil)
	resp.Body.Close()
	resp = tc.postHX("/report", url.Values{"target_kind": {"user"}, "target_id": {reporter.ID.String()}, "reason": {"Retaliation report"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unsuspended user's write: %d, want 200", resp.StatusCode)
	}

	// Mark Invalid (reason required) → state + event row with the admin as actor.
	resp = ac.postForm("/admin/problems/"+pid.String()+"/invalid", url.Values{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid without reason: %d", resp.StatusCode)
	}
	resp = ac.postForm("/admin/problems/"+pid.String()+"/invalid", url.Values{"reason": {"Spam"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("mark invalid: %d", resp.StatusCode)
	}
	if st := app.modScalar(t, `SELECT state::text FROM problems WHERE id = $1`, pid); st != "invalid" {
		t.Fatalf("state = %v", st)
	}
	if n := app.modScalar(t, `SELECT count(*) FROM problem_state_events WHERE problem_id = $1 AND actor_id = $2 AND to_state = 'invalid' AND from_state = 'open'`, pid, admin.ID); n != int64(1) {
		t.Fatalf("invalid events = %v", n)
	}
	resp = ac.postForm("/admin/problems/"+pid.String()+"/reopen", nil)
	resp.Body.Close()
	if st := app.modScalar(t, `SELECT state::text FROM problems WHERE id = $1`, pid); resp.StatusCode != http.StatusSeeOther || st != "open" {
		t.Fatalf("reopen: %d state=%v", resp.StatusCode, st)
	}
	resp = ac.postForm("/admin/problems/"+pid.String()+"/meta", nil)
	resp.Body.Close()
	if on := app.modScalar(t, `SELECT on_meta_board FROM problems WHERE id = $1`, pid); on != true {
		t.Fatalf("meta toggle: %v", on)
	}
	resp = ac.postForm("/admin/problems/not-a-uuid/meta", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad problem id: %d", resp.StatusCode)
	}

	// Zero votes → weights 0 and a RescoreAll job for the problem.
	app.modExec(t, `INSERT INTO problem_revision_votes (revision_id, user_id, weight) VALUES ($1, $2, 1)`, rid, troll.ID)
	resp = ac.postForm("/admin/users/"+troll.Handle+"/zero-votes", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("zero votes: %d", resp.StatusCode)
	}
	if w := app.modScalar(t, `SELECT weight::float8 FROM problem_revision_votes WHERE user_id = $1`, troll.ID); w != float64(0) {
		t.Fatalf("weight = %v", w)
	}
	if n := app.modScalar(t, `SELECT count(*) FROM river_job WHERE kind = 'rescore_all' AND args->'problem_ids' ? $1::text`, pid.String()); n != int64(1) {
		t.Fatalf("rescore jobs for problem = %v", n)
	}
	resp = ac.postForm("/admin/rescore", nil)
	resp.Body.Close()
	if n := app.modScalar(t, `SELECT count(*) FROM river_job WHERE kind = 'rescore_all' AND NOT (args ? 'problem_ids')`); resp.StatusCode != http.StatusSeeOther || n.(int64) < 1 {
		t.Fatalf("full rescore: %d jobs=%v", resp.StatusCode, n)
	}

	// Resolve every open report; the list empties.
	rows, err := app.Store.Pool.Query(context.Background(), `SELECT id FROM reports WHERE resolved_at IS NULL`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) != 4 {
		t.Fatalf("open reports = %d, want 4", len(ids))
	}
	for _, id := range ids {
		resp = ac.postForm("/admin/reports/"+id.String()+"/resolve", nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("resolve: %d", resp.StatusCode)
		}
	}
	page = body(t, ac.get("/admin"))
	if !strings.Contains(page, "No open reports") {
		t.Fatal("reports still listed after resolving")
	}
}
