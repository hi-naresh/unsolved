package http

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/jobs"
)

// postmarkInbox is a fake Postmark API that keeps every email it is sent.
type postmarkInbox struct {
	mu     sync.Mutex
	emails []jobs.PostmarkEmail
	tokens []string
}

func newPostmarkInbox(t *testing.T) *postmarkInbox {
	t.Helper()
	in := &postmarkInbox{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var e jobs.PostmarkEmail
		if r.URL.Path != "/email" || json.Unmarshal(b, &e) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		in.mu.Lock()
		in.emails = append(in.emails, e)
		in.tokens = append(in.tokens, r.Header.Get("X-Postmark-Server-Token"))
		in.mu.Unlock()
		_, _ = w.Write([]byte(`{"ErrorCode":0,"Message":"OK"}`))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(jobs.SetPostmarkBaseURL(srv.URL))
	return in
}

func (in *postmarkInbox) all() ([]jobs.PostmarkEmail, []string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	return append([]jobs.PostmarkEmail(nil), in.emails...), append([]string(nil), in.tokens...)
}

var outcomeLinkRe = regexp.MustCompile(`http://unsolved\.test(/solved/[A-Za-z0-9_-]+)\?outcome=(worked|partly|failed)`)

// Phase 3 acceptance: optional email in settings; votes push a solution over
// SOFT_SOLVED_THRESHOLD → one SolvedPrompt email; the one-tap link shows a
// confirm page without changing anything; confirming records the outcome
// and (for "worked") marks the problem solved with a state event.
func TestPhase3SolvedPrompt(t *testing.T) {
	inbox := newPostmarkInbox(t)
	app := newTestApp(t, func(c *config.Config) { c.SoftSolvedThreshold = 2; c.PostmarkToken = "test" })
	poster, helper := app.newUser(t), app.newUser(t)
	pc := app.as(t, poster)

	// The poster adds an email in settings.
	mustStatus(t, pc.postForm("/settings", url.Values{"handle": {poster.Handle}, "email": {"poster@example.com"}}), http.StatusSeeOther)

	pid := postProblem(t, pc, problemForm("Phase three: re-keying orders into the ERP"))
	resp := app.as(t, helper).postForm("/p/"+pid+"/solutions", solutionForm("off_the_shelf"))
	mustStatus(t, resp, http.StatusSeeOther)
	loc := resp.Header.Get("Location")
	sid := loc[strings.Index(loc, "#s-")+3:]
	var srev string
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT current_revision_id FROM solutions WHERE id = $1`, sid).Scan(&srev); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		mustStatus(t, app.as(t, app.newUser(t)).postHX("/sr/"+srev+"/vote", nil), http.StatusOK)
	}

	// Exactly one well-formed email reaches Postmark.
	eventually(t, 15*time.Second, func() bool { e, _ := inbox.all(); return len(e) > 0 })
	time.Sleep(500 * time.Millisecond) // give a duplicate the chance to show up
	emails, tokens := inbox.all()
	if len(emails) != 1 {
		t.Fatalf("%d emails sent, want 1", len(emails))
	}
	e := emails[0]
	if tokens[0] != "test" || e.From != "no-reply@unsolved.test" || e.To != "poster@example.com" || e.MessageStream != "outbound" ||
		!strings.Contains(e.Subject, "Phase three: re-keying orders into the ERP") {
		t.Fatalf("bad email: %+v", e)
	}
	if !strings.Contains(e.TextBody, `A solution to your problem "Phase three: re-keying orders into the ERP" has enough votes to count as solved. Did it work for you?`) {
		t.Fatalf("text body: %s", e.TextBody)
	}
	links := outcomeLinkRe.FindAllStringSubmatch(e.TextBody, -1)
	if len(links) != 3 {
		t.Fatalf("links: %v", links)
	}
	path := links[0][1] // worked
	if links[0][2] != "worked" {
		t.Fatalf("first link is %s", links[0][2])
	}

	// GET (as a mail scanner would, signed out) shows the confirm page and changes nothing.
	c := app.anon(t)
	resp = c.get(path + "?outcome=worked")
	if resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("confirm page headers: %v", resp.Header)
	}
	b := mustStatus(t, resp, http.StatusOK)
	for _, want := range []string{"Did it work?", "Phase three: re-keying orders into the ERP", "map the spreadsheet columns once",
		`action="` + path + `"`, `name="outcome" value="worked"`, "Confirm: Worked", "This also marks your problem solved."} {
		if !strings.Contains(b, want) {
			t.Fatalf("confirm page lacks %q", want)
		}
	}
	b = mustStatus(t, c.get(path+"?outcome=bogus"), http.StatusOK)
	for _, o := range []string{"worked", "partly", "failed"} {
		if !strings.Contains(b, `name="outcome" value="`+o+`"`) {
			t.Fatalf("choice page lacks %s", o)
		}
	}
	assertOutcome(t, app, pid, sid, "open", "", 0)

	// POST needs the CSRF token like every other.
	req, _ := http.NewRequest(http.MethodPost, app.Srv.URL+path, strings.NewReader("outcome=worked"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mustStatus(t, c.do(req), http.StatusForbidden)
	assertOutcome(t, app, pid, sid, "open", "", 0)

	// Confirm: trial recorded, problem solved, one event by the poster.
	resp = c.postForm(path, url.Values{"outcome": {"worked"}})
	mustStatus(t, resp, http.StatusSeeOther)
	if got := resp.Header.Get("Location"); got != "/p/"+pid+"#s-"+sid {
		t.Fatalf("redirect to %q", got)
	}
	assertOutcome(t, app, pid, sid, "solved", "worked", 1)
	var actor uuid.UUID
	var reason string
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT actor_id, reason FROM problem_state_events WHERE problem_id = $1`, pid).Scan(&actor, &reason); err != nil {
		t.Fatal(err)
	}
	if actor != poster.ID || reason != "Poster confirmed via email" {
		t.Fatalf("event actor %v reason %q", actor, reason)
	}
	b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, "Status: Solved") || !strings.Contains(b, "Poster confirmed via email") {
		t.Fatal("problem page does not show the solved state and reason")
	}
	// Tapping again is harmless.
	mustStatus(t, c.postForm(path, url.Values{"outcome": {"worked"}}), http.StatusSeeOther)
	assertOutcome(t, app, pid, sid, "solved", "worked", 1)

	// Bad and expired links get friendly pages; a suspended poster is refused.
	b = mustStatus(t, c.get("/solved/not-a-real-token?outcome=worked"), http.StatusBadRequest)
	if !strings.Contains(b, "This link doesn") {
		t.Fatalf("bad link page: %s", b)
	}
	mustStatus(t, c.postForm("/solved/not-a-real-token", url.Values{"outcome": {"worked"}}), http.StatusBadRequest)
	expired := app.Svc.MakeOutcomeToken(jobs.OutcomeClaims{
		ProblemID: uuid.MustParse(pid), SolutionID: uuid.MustParse(sid), PosterID: poster.ID, Expires: time.Now().Add(-time.Hour),
	})
	b = mustStatus(t, c.get("/solved/"+expired+"?outcome=partly"), http.StatusGone)
	if !strings.Contains(b, "This link has expired") {
		t.Fatalf("expired page: %s", b)
	}
	mustStatus(t, c.postForm("/solved/"+expired, url.Values{"outcome": {"partly"}}), http.StatusGone)
	execSQL(t, app, `UPDATE users SET suspended_at = now() WHERE id = $1`, poster.ID)
	mustStatus(t, c.get(path+"?outcome=partly"), http.StatusForbidden)
	mustStatus(t, c.postForm(path, url.Values{"outcome": {"partly"}}), http.StatusForbidden)
	assertOutcome(t, app, pid, sid, "solved", "worked", 1)
}

// A poster without an email gets no SolvedPrompt; the job still completes.
func TestPhase3NoEmailNoPrompt(t *testing.T) {
	inbox := newPostmarkInbox(t)
	app := newTestApp(t, func(c *config.Config) { c.SoftSolvedThreshold = 1; c.PostmarkToken = "test" })
	poster, helper := app.newUser(t), app.newUser(t)
	pid := postProblem(t, app.as(t, poster), problemForm("Phase three: nobody to email about this"))
	resp := app.as(t, helper).postForm("/p/"+pid+"/solutions", solutionForm("process_change"))
	mustStatus(t, resp, http.StatusSeeOther)
	var srev string
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT current_revision_id FROM solutions WHERE problem_id = $1`, pid).Scan(&srev); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, app.as(t, app.newUser(t)).postHX("/sr/"+srev+"/vote", nil), http.StatusOK)
	eventually(t, 15*time.Second, func() bool {
		var state string
		err := app.Store.Pool.QueryRow(t.Context(),
			`SELECT state::text FROM river_job WHERE kind = 'solved_prompt' AND args->>'problem_id' = $1`, pid).Scan(&state)
		return err == nil && state == "completed"
	})
	if e, _ := inbox.all(); len(e) != 0 {
		t.Fatalf("%d emails sent to a poster without an email", len(e))
	}
}

func assertOutcome(t *testing.T, app *testApp, pid, sid, wantState, wantTrial string, wantEvents int) {
	t.Helper()
	var state, trial, note string
	var events int
	if err := app.Store.Pool.QueryRow(t.Context(), `
		SELECT p.state::text,
		       COALESCE((SELECT outcome::text FROM solution_trials WHERE solution_id = $2 AND user_id = p.author_id), ''),
		       COALESCE((SELECT note FROM solution_trials WHERE solution_id = $2 AND user_id = p.author_id), ''),
		       (SELECT count(*) FROM problem_state_events WHERE problem_id = p.id)::int
		FROM problems p WHERE p.id = $1`, pid, sid).Scan(&state, &trial, &note, &events); err != nil {
		t.Fatal(err)
	}
	if state != wantState || trial != wantTrial || events != wantEvents {
		t.Fatalf("state %s trial %q events %d; want %s %q %d", state, trial, events, wantState, wantTrial, wantEvents)
	}
	if trial != "" && note != "via email" {
		t.Fatalf("trial note %q", note)
	}
}
