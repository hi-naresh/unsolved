package http

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// userSeedProblem writes a problem with one revision by author. It uses COPY
// in one transaction (no SQL outside sqlc files; the deferred
// current_revision_id FK is checked at commit). Once the problems service is
// merged, tests can use it instead.
func userSeedProblem(t *testing.T, app *testApp, author uuid.UUID, display, title string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	pid, rid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	err := pgx.BeginFunc(ctx, app.Store.Pool, func(tx pgx.Tx) error {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"problems"},
			[]string{"id", "domain_id", "author_id", "author_display", "current_revision_id"},
			pgx.CopyFromRows([][]any{{pid, int16(1), author, display, rid}})); err != nil {
			return err
		}
		_, err := tx.CopyFrom(ctx, pgx.Identifier{"problem_revisions"},
			[]string{"id", "problem_id", "author_id", "author_display", "title", "current_process", "pain", "tried"},
			pgx.CopyFromRows([][]any{{rid, pid, author, display, title,
				strings.Repeat("Step by step we do the thing. ", 3), "It takes far too long every week.", "nothing yet"}}))
		return err
	})
	if err != nil {
		t.Fatalf("seed problem: %v", err)
	}
	return pid
}

// userWelcome gives u a known handle and directory choice.
func userWelcome(t *testing.T, app *testApp, u store.User, inDirectory bool) string {
	t.Helper()
	h := fmt.Sprintf("u%d", time.Now().UnixNano()%1e12)
	if err := app.Svc.CompleteWelcome(context.Background(), u.ID, h, inDirectory); err != nil {
		t.Fatal(err)
	}
	return h
}

func TestWelcome(t *testing.T) {
	app := newTestApp(t)

	resp := app.anon(t).get("/welcome")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/signin?next=") {
		t.Fatalf("signed-out /welcome: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	taken := userWelcome(t, app, app.newUser(t), false)
	u := app.newUser(t)
	c := app.as(t, u)
	tests := []struct {
		name, handle, wantMsg string
	}{
		{"too short", "ab", "3–24 characters"},
		{"bad chars", "bad-handle", "3–24 characters"},
		{"reserved", "admin", "reserved"},
		{"deleted prefix", "deleted_12345678", "reserved"},
		{"taken", taken, "taken"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := c.postForm("/welcome", url.Values{"handle": {tc.handle}, "next": {"/problems"}})
			b := body(t, resp)
			if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(b, tc.wantMsg) {
				t.Fatalf("want 422 with %q, got %d\n%s", tc.wantMsg, resp.StatusCode, b)
			}
			if !strings.Contains(b, `value="/problems"`) {
				t.Fatal("next lost on re-render")
			}
		})
	}
	h := fmt.Sprintf("ok_%d", time.Now().UnixNano()%1e9)
	resp = c.postForm("/welcome", url.Values{"handle": {h}, "next": {"//evil.example"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Fatalf("welcome with unsafe next: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	got, _ := app.Svc.GetUser(context.Background(), u.ID)
	if got.Handle != h || got.InDirectory {
		t.Fatalf("after welcome: handle=%q dir=%v (directory must default off)", got.Handle, got.InDirectory)
	}
}

func TestContributorWelcomeFlow(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t)
	c := app.as(t, u)
	returnPath := "/p/123/revise?from=preview"
	resp := c.postForm("/welcome", url.Values{"action": {"skip"}, "handle": {"different"}, "next": {returnPath}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != returnPath {
		t.Fatalf("skip return: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	got, _ := app.Svc.GetUser(context.Background(), u.ID)
	if got.Handle != u.Handle || got.ContributionPreference != "" || got.OnboardingCompleted {
		t.Fatalf("skip changed profile: %+v", got)
	}
	resp = c.postForm("/welcome", url.Values{"handle": {"new_handle"}, "contribution_preference": {"invalid"}, "next": {returnPath}})
	b := body(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(b, "Choose a valid way") || !strings.Contains(b, `value="/p/123/revise?from=preview"`) {
		t.Fatalf("invalid choice: %d %s", resp.StatusCode, b)
	}
	got, _ = app.Svc.GetUser(context.Background(), u.ID)
	if got.Handle != u.Handle {
		t.Fatal("invalid choice changed handle")
	}
	resp = c.postForm("/welcome", url.Values{"handle": {"new_handle"}, "contribution_preference": {"identifier"}, "next": {returnPath}})
	resp.Body.Close()
	if resp.Header.Get("Location") != returnPath {
		t.Fatalf("specific return lost: %q", resp.Header.Get("Location"))
	}
	got, _ = app.Svc.GetUser(context.Background(), u.ID)
	if got.ContributionPreference != "identifier" || !got.OnboardingCompleted {
		t.Fatalf("not saved: %+v", got)
	}
	other := app.newUser(t)
	resp = app.as(t, other).postForm("/welcome", url.Values{"handle": {other.Handle}, "contribution_preference": {"solver"}, "next": {"/"}})
	resp.Body.Close()
	if resp.Header.Get("Location") != "/problems" {
		t.Fatalf("solver destination: %q", resp.Header.Get("Location"))
	}
}

func TestProfilePage(t *testing.T) {
	app := newTestApp(t)
	u := app.newUser(t) // identity profile URL https://x.com/someone
	handle := userWelcome(t, app, u, false)
	named := userSeedProblem(t, app, u.ID, "named", "A named problem about invoices")
	anon := userSeedProblem(t, app, u.ID, "anonymous", "A secret anonymous problem")

	c := app.anon(t)
	resp := c.get("/u/" + handle)
	b := body(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile: %d", resp.StatusCode)
	}
	if !strings.Contains(b, "A named problem about invoices") || !strings.Contains(b, "/p/"+named.String()) {
		t.Fatal("named contribution missing")
	}
	if strings.Contains(b, "secret anonymous") || strings.Contains(b, anon.String()) {
		t.Fatal("anonymous contribution shown on profile")
	}
	if strings.Contains(b, u.ID.String()) {
		t.Fatal("user id leaked into profile page")
	}
	if strings.Contains(b, "x.com/someone") {
		t.Fatal("social link shown while not in directory")
	}

	// Declared history is labelled unverified; links appear once in the directory.
	if err := app.Svc.UpdateSettings(context.Background(), u.ID, svcSettingsFor(handle, true, "Ran a clinic for ten years")); err != nil {
		t.Fatal(err)
	}
	b = body(t, c.get("/u/"+handle))
	if !strings.Contains(b, "https://x.com/someone") {
		t.Fatal("social link missing while in directory")
	}
	if !strings.Contains(b, "Ran a clinic for ten years") || !strings.Contains(b, "unverified, carries no weight") {
		t.Fatal("declared history not shown with its label")
	}

	resp = c.get("/u/no_such_user")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing user: %d", resp.StatusCode)
	}
	if err := app.Svc.DeleteAccount(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{handle, "deleted_" + strings.ReplaceAll(u.ID.String(), "-", "")[24:]} {
		resp = c.get("/u/" + h)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("deleted user /u/%s: %d", h, resp.StatusCode)
		}
	}
}

func TestMembersPage(t *testing.T) {
	app := newTestApp(t)
	listed := userWelcome(t, app, app.newUser(t), true)
	unlisted := userWelcome(t, app, app.newUser(t), false)
	gone := app.newUser(t)
	goneHandle := userWelcome(t, app, gone, true)
	if err := app.Svc.DeleteAccount(context.Background(), gone.ID); err != nil {
		t.Fatal(err)
	}

	// Walk every page: keyset pagination must terminate and cover everyone listed.
	var all strings.Builder
	path := "/members"
	for i := 0; ; i++ {
		resp := app.anon(t).get(path)
		b := body(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		all.WriteString(b)
		j := strings.Index(b, `href="/members?after=`)
		if j < 0 {
			break
		}
		rest := b[j+len(`href="`):]
		path = strings.ReplaceAll(rest[:strings.Index(rest, `"`)], "&amp;", "&")
		if i > 50 {
			t.Fatal("pagination does not terminate")
		}
	}
	got := all.String()
	if !strings.Contains(got, "@"+listed) {
		t.Fatal("opted-in member missing")
	}
	if strings.Contains(got, "@"+unlisted) || strings.Contains(got, goneHandle) || strings.Contains(got, "deleted_") {
		t.Fatal("unlisted or deleted member shown")
	}
	resp := app.anon(t).get("/members?after=garbage")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad cursor: %d", resp.StatusCode)
	}
}
