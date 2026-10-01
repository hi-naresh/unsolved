package http

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hi-naresh/unsolved/internal/store"
)

func solutionForm(kind string) url.Values {
	return url.Values{
		"kind":    {kind},
		"body":    {"Use the ERP's CSV import: map the spreadsheet columns once, then import every Monday."},
		"display": {"named"},
	}
}

// Phase 2 acceptance: solutions with kinds, revisions and votes; "tried it";
// poster state changes with history; soft_solved and the front page;
// founder interest; meta board; invalid problems.
func TestPhase2Solutions(t *testing.T) {
	app := newTestApp(t)
	poster, helper, other := app.newUser(t), app.newUser(t), app.newUser(t)
	pc, hc, oc := app.as(t, poster), app.as(t, helper), app.as(t, other)
	pid := postProblem(t, pc, problemForm("Phase two: re-keying orders into the ERP"))

	// Post a solution; bad body → 422 with input kept.
	bad := solutionForm("off_the_shelf")
	bad.Set("body", "Use Excel.")
	b := mustStatus(t, hc.postForm("/p/"+pid+"/solutions", bad), http.StatusUnprocessableEntity)
	if !strings.Contains(b, "Use Excel.") || !strings.Contains(b, `id="body-error"`) {
		t.Fatal("solution 422 lacks input or message")
	}
	resp := hc.postForm("/p/"+pid+"/solutions", solutionForm("off_the_shelf"))
	mustStatus(t, resp, http.StatusSeeOther)
	loc := resp.Header.Get("Location")
	sid := loc[strings.Index(loc, "#s-")+3:]
	var srev string
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT current_revision_id FROM solutions WHERE id = $1`, sid).Scan(&srev); err != nil {
		t.Fatal(err)
	}
	b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, "Use an existing tool") || !strings.Contains(b, "map the spreadsheet columns once") || !strings.Contains(b, "@"+helper.Handle) {
		t.Fatal("problem page lacks the solution")
	}
	// The post form offers every kind.
	b = mustStatus(t, oc.get("/p/"+pid), http.StatusOK)
	for _, want := range []string{"Change the process", "Use an existing tool", "Build something custom", "Don&#39;t automate this", `action="/p/` + pid + `/solutions"`} {
		if !strings.Contains(b, want) {
			t.Fatalf("solution form lacks %q", want)
		}
	}

	// Solution revisions need a why-note.
	sr := url.Values{"parent_revision_id": {srev}, "body": {solutionForm("").Get("body") + " Keep the mapping in a sheet."}}
	mustStatus(t, oc.postForm("/s/"+sid+"/revisions", sr), http.StatusUnprocessableEntity)
	sr.Set("why_note", "Added the mapping sheet.")
	mustStatus(t, oc.postForm("/s/"+sid+"/revisions", sr), http.StatusSeeOther)

	// Solution votes: self-vote forbidden, toggle partial otherwise.
	mustStatus(t, hc.postHX("/sr/"+srev+"/vote", nil), http.StatusForbidden)
	b = mustStatus(t, oc.postHX("/sr/"+srev+"/vote", nil), http.StatusOK)
	if !strings.Contains(b, `aria-pressed="true"`) || !strings.Contains(b, `hx-post="/sr/`+srev+`/vote"`) {
		t.Fatalf("solution vote partial: %s", b)
	}

	// Tried it: counts and recent notes; one outcome per user.
	mustStatus(t, oc.postForm("/s/"+sid+"/tried", url.Values{"outcome": {"failed"}, "note": {"No import in our ERP."}}), http.StatusSeeOther)
	mustStatus(t, oc.postForm("/s/"+sid+"/tried", url.Values{"outcome": {"worked"}, "note": {"Found the import in settings."}}), http.StatusSeeOther)
	mustStatus(t, pc.postForm("/s/"+sid+"/tried", url.Values{"outcome": {"partly"}}), http.StatusSeeOther)
	mustStatus(t, pc.postForm("/s/"+sid+"/tried", url.Values{"outcome": {"sort of"}}), http.StatusUnprocessableEntity)
	b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK)
	for _, want := range []string{`aria-label="1 worked, 1 partly, 0 didn&#39;t work"`, "Found the import in settings."} {
		if !strings.Contains(b, want) {
			t.Fatalf("outcomes lack %q", want)
		}
	}
	if strings.Contains(b, "No import in our ERP.") {
		t.Fatal("replaced trial note still shown")
	}

	// Founder interest toggle + count.
	b = mustStatus(t, oc.postHX("/p/"+pid+"/interest", nil), http.StatusOK)
	if !strings.Contains(b, "1 founder interested") {
		t.Fatalf("interest partial: %s", b)
	}
	mustStatus(t, hc.postForm("/p/"+pid+"/interest", nil), http.StatusSeeOther)
	if b = mustStatus(t, app.anon(t).get("/p/"+pid), http.StatusOK); !strings.Contains(b, "2 founders interested") {
		t.Fatal("interest count not shown")
	}

	// State: only the poster; every change is in the history.
	mustStatus(t, oc.postForm("/p/"+pid+"/state", url.Values{"to": {"solved"}}), http.StatusForbidden)
	mustStatus(t, pc.postForm("/p/"+pid+"/state", url.Values{"to": {"solved"}, "reason": {"The CSV import did it."}}), http.StatusSeeOther)
	b = mustStatus(t, pc.get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, `chip chip-solved">Solved</span>`) || !strings.Contains(b, "by the poster") || !strings.Contains(b, "The CSV import did it.") || !strings.Contains(b, "Reopen") {
		t.Fatal("solved state or history not shown")
	}
	mustStatus(t, pc.postForm("/p/"+pid+"/state", url.Values{"to": {"open"}}), http.StatusSeeOther)
	var events int
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT count(*) FROM problem_state_events WHERE problem_id = $1`, pid).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Fatalf("%d state events", events)
	}
	mustStatus(t, pc.postForm("/p/"+pid+"/state", url.Values{"to": {"invalid"}}), http.StatusUnprocessableEntity)
}

func TestPhase2SoftSolvedMetaInvalid(t *testing.T) {
	app := newTestApp(t)
	poster, helper := app.newUser(t), app.newUser(t)
	pid := problemDirect(t, app, poster.ID, "Soft-solved: chasing delivery status by phone", store.DisplayModeNamed).String()
	resp := app.as(t, helper).postForm("/p/"+pid+"/solutions", solutionForm("process_change"))
	mustStatus(t, resp, http.StatusSeeOther)
	var srev string
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT current_revision_id FROM solutions WHERE problem_id = $1`, pid).Scan(&srev); err != nil {
		t.Fatal(err)
	}
	c := app.anon(t)
	if countOf(walkList(t, c, "/"), pid) != 1 {
		t.Fatal("open problem not on the front page")
	}
	var voters []*testClient
	for range 5 {
		v := app.as(t, app.newUser(t))
		voters = append(voters, v)
		mustStatus(t, v.postHX("/sr/"+srev+"/vote", nil), http.StatusOK)
	}
	eventually(t, 10*time.Second, func() bool {
		return strings.Contains(body(t, c.get("/p/"+pid)), ">Likely solved</span>")
	})
	if countOf(walkList(t, c, "/"), pid) != 0 {
		t.Fatal("soft-solved problem still on the front page")
	}
	var prompts int
	if err := app.Store.Pool.QueryRow(t.Context(), `SELECT count(*) FROM river_job WHERE kind = 'solved_prompt' AND args->>'problem_id' = $1`, pid).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("SolvedPrompt enqueued %d times", prompts)
	}
	// Unvote → back under the threshold → back on the front page.
	mustStatus(t, voters[0].postHX("/sr/"+srev+"/vote", nil), http.StatusOK)
	eventually(t, 10*time.Second, func() bool {
		return !strings.Contains(body(t, c.get("/p/"+pid)), ">Likely solved</span>")
	})
	if countOf(walkList(t, c, "/"), pid) != 1 {
		t.Fatal("problem not back on the front page")
	}

	// Meta board: listed on /meta, not on the front page or /problems.
	meta := problemDirect(t, app, poster.ID, "Meta: the posting form is too long", store.DisplayModeNamed).String()
	execSQL(t, app, `UPDATE problems SET on_meta_board = true WHERE id = $1`, meta)
	if countOf(walkList(t, c, "/meta"), meta) != 1 {
		t.Fatal("meta problem not on /meta")
	}
	if countOf(walkList(t, c, "/"), meta) != 0 || countOf(walkList(t, c, "/problems?sort=new"), meta) != 0 {
		t.Fatal("meta problem on the main lists")
	}

	// Invalid: a notice, no write controls, writes refused.
	execSQL(t, app, `UPDATE problems SET state = 'invalid' WHERE id = $1`, pid)
	hc := app.as(t, helper)
	b := mustStatus(t, hc.get("/p/"+pid), http.StatusOK)
	if !strings.Contains(b, "marked invalid") || strings.Contains(b, `href="/p/`+pid+`/revise"`) || strings.Contains(b, `action="/p/`+pid+`/solutions"`) || strings.Contains(b, "hx-post=\"/sr/") {
		t.Fatal("invalid problem still shows write controls")
	}
	mustStatus(t, voters[1].postHX("/sr/"+srev+"/vote", nil), http.StatusForbidden)
	mustStatus(t, hc.postForm("/p/"+pid+"/solutions", solutionForm("dont_automate")), http.StatusForbidden)
	if countOf(walkList(t, c, "/problems?state=invalid"), pid) != 1 {
		t.Fatal("state filter misses the invalid problem")
	}
}
