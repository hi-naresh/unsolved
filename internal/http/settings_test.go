package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hi-naresh/unsolved/internal/auth"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

func svcSettingsFor(handle string, inDirectory bool, history string) service.Settings {
	return service.Settings{Handle: handle, InDirectory: inDirectory, DeclaredHistory: history}
}

func TestSettingsUpdate(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t)
	c := app.as(t, u)

	resp := app.anon(t).get("/settings")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signed-out /settings: %d", resp.StatusCode)
	}

	resp = c.get("/settings")
	if b := body(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(b, u.Handle) || !strings.Contains(b, `name="csrf_token"`) {
		t.Fatalf("settings page: %d", resp.StatusCode)
	}

	handle := fmt.Sprintf("set_%d", time.Now().UnixNano()%1e9)
	resp = c.postForm("/settings", url.Values{
		"handle": {handle}, "in_directory": {"1"},
		"declared_history": {"Ran dispatch at a haulage firm."}, "email": {"me@example.com"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/settings?saved=1" {
		t.Fatalf("save: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	got, _ := app.Svc.GetUser(context.Background(), u.ID)
	if got.Handle != handle || !got.InDirectory || got.DeclaredHistory == nil || got.Email == nil || *got.Email != "me@example.com" {
		t.Fatalf("not saved: %+v", got)
	}

	tests := []struct {
		name    string
		form    url.Values
		wantMsg string
	}{
		{"bad email", url.Values{"handle": {handle}, "email": {"nope"}}, "doesn&#39;t look like an email"},
		{"history too long", url.Values{"handle": {handle}, "declared_history": {strings.Repeat("a", 2001)}}, "2000 characters"},
		{"bad handle", url.Values{"handle": {"x"}}, "3–24 characters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := c.postForm("/settings", tc.form)
			b := body(t, resp)
			if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(b, tc.wantMsg) {
				t.Fatalf("want 422 with %q, got %d\n%s", tc.wantMsg, resp.StatusCode, b)
			}
		})
	}
	// Nothing changed by the rejected posts; empty email clears it.
	resp = c.postForm("/settings", url.Values{"handle": {handle}, "email": {""}})
	resp.Body.Close()
	got, _ = app.Svc.GetUser(context.Background(), u.ID)
	if got.Email != nil || got.InDirectory {
		t.Fatalf("clear: %+v", got)
	}

	// Missing CSRF token is refused.
	req, _ := http.NewRequest(http.MethodPost, app.Srv.URL+"/settings", strings.NewReader("handle=zzz_zzz"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp = c.do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", resp.StatusCode)
	}
}

func TestSettingsContributorPreference(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t)
	c := app.as(t, u)
	resp := c.get("/settings")
	b := body(t, resp)
	if !strings.Contains(b, "Choose how you would like to begin") || !strings.Contains(b, `name="contribution_preference"`) {
		t.Fatal("existing account was not offered optional setup")
	}
	resp = c.postForm("/settings", url.Values{"handle": {u.Handle}, "contribution_preference": {"both"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("save: %d", resp.StatusCode)
	}
	got, _ := app.Svc.GetUser(context.Background(), u.ID)
	if got.ContributionPreference != "both" || !got.OnboardingCompleted {
		t.Fatalf("preference not saved: %+v", got)
	}
	resp = c.postForm("/settings", url.Values{"handle": {u.Handle}, "contribution_preference": {"bogus"}})
	b = body(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(b, "Choose a valid way") {
		t.Fatalf("invalid preference: %d %s", resp.StatusCode, b)
	}
	got, _ = app.Svc.GetUser(context.Background(), u.ID)
	if got.ContributionPreference != "both" {
		t.Fatal("invalid update changed preference")
	}
}

func TestExport(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t)
	pid := userSeedProblem(t, app, u.ID, "anonymous", "An anonymous problem to export")
	other := app.newUser(t)
	userSeedProblem(t, app, other.ID, "named", "Someone else's problem here")

	resp := app.anon(t).get("/settings/export")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("signed-out export: %d", resp.StatusCode)
	}

	resp = app.as(t, u).get("/settings/export")
	b := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("export: %d %v", resp.StatusCode, resp.Header)
	}
	var ex map[string]json.RawMessage
	if err := json.Unmarshal([]byte(b), &ex); err != nil {
		t.Fatalf("export not JSON: %v", err)
	}
	for _, k := range []string{"user", "identities", "problems", "problem_revisions", "solutions", "solution_revisions",
		"problem_revision_votes", "solution_revision_votes", "solution_trials", "founder_interest", "reports_filed", "vouches_given"} {
		if _, ok := ex[k]; !ok {
			t.Errorf("export lacks %q", k)
		}
	}
	if !strings.Contains(string(ex["problems"]), pid.String()) || !strings.Contains(string(ex["problems"]), `"anonymous"`) ||
		!strings.Contains(string(ex["problem_revisions"]), "An anonymous problem to export") {
		t.Fatalf("anonymous post missing from export: %s", b)
	}
	if strings.Contains(b, "Someone else's problem") {
		t.Fatal("export contains another user's content")
	}
	if !strings.Contains(string(ex["identities"]), `"provider_uid"`) || !strings.Contains(string(ex["identities"]), "https://x.com/someone") {
		t.Fatalf("identities: %s", ex["identities"])
	}
}

func TestDeleteAccount(t *testing.T) {
	app, f := newAuthTestApp(t)
	g := fakeGrant{uid: fmt.Sprintf("del-%d", time.Now().UnixNano()), name: "Leaving Soon", username: "leaving"}
	c := app.anon(t)
	resp := f.signIn(c, "x", "/", g)
	resp.Body.Close()
	handle := fmt.Sprintf("bye_%d", time.Now().UnixNano()%1e9)
	resp = c.postForm("/welcome", url.Values{"handle": {handle}, "in_directory": {"1"}, "next": {"/"}})
	resp.Body.Close()
	// Find the user id the way the app does: via the session.
	var token string
	su, _ := url.Parse(app.Srv.URL)
	for _, ck := range c.http.Jar.Cookies(su) {
		if ck.Name == auth.SessionCookie {
			token = ck.Value
		}
	}
	u, err := app.Svc.SessionUser(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	pid := userSeedProblem(t, app, u.ID, "named", "A problem that should outlive its author")
	_ = app.Svc.UpdateSettings(context.Background(), u.ID, service.Settings{Handle: handle, InDirectory: true, DeclaredHistory: "hist", Email: "gone@example.com"})
	// A second session (another device) must end too.
	other := app.as(t, u)

	resp = c.get("/settings/delete")
	if b := body(t, resp); resp.StatusCode != http.StatusOK || !strings.Contains(b, `action="/settings/delete"`) {
		t.Fatalf("confirm page: %d", resp.StatusCode)
	}
	resp = c.postForm("/settings/delete", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("delete: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if authHasSession(c) {
		t.Fatal("session cookie not cleared")
	}
	for _, cl := range []*testClient{c, other} {
		resp = cl.get("/settings")
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("still signed in after deletion: %d", resp.StatusCode)
		}
	}

	scrubbed, err := app.Store.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(scrubbed.Handle, "deleted_") || len(scrubbed.Handle) != 16 || scrubbed.DisplayName != "deleted user" ||
		scrubbed.Email != nil || scrubbed.DeclaredHistory != nil || scrubbed.InDirectory || scrubbed.DeletedAt == nil {
		t.Fatalf("not scrubbed: %+v", scrubbed)
	}
	// Content survives, still attributed (as "deleted user") to the row.
	rows, err := app.Store.ListNamedContributions(context.Background(), store.ListNamedContributionsParams{UserID: u.ID, MaxRows: 30})
	if err != nil || len(rows) != 1 || rows[0].ProblemID != pid {
		t.Fatalf("content did not survive: %v %+v", err, rows)
	}
	resp = app.anon(t).get("/u/" + handle)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("old profile: %d", resp.StatusCode)
	}

	// The same X account signing in again gets a fresh, unrelated account.
	c2 := app.anon(t)
	resp = f.signIn(c2, "x", "/", g)
	resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Location"), "/welcome") {
		t.Fatalf("re-sign-in after deletion: %q", resp.Header.Get("Location"))
	}
}
