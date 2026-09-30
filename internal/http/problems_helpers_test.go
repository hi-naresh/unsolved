package http

// Helpers for the content e2e tests (phase1_test.go, phase2_test.go).

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

func problemForm(title string) url.Values {
	return url.Values{
		"domain_id":       {"1"},
		"current_process": {"Every Monday we export the orders to a spreadsheet,\r\nthen re-key them into the ERP by hand."},
		"pain":            {"It takes six hours and typos cause wrong shipments."},
		"tried":           {"nothing yet"},
		"title":           {title},
		"display":         {"named"},
	}
}

// postProblem posts the guided form and returns the new problem id.
func postProblem(t *testing.T, c *testClient, form url.Values) string {
	t.Helper()
	resp := c.postForm("/problems", form)
	b := body(t, resp)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("post problem: %d %s", resp.StatusCode, b)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/p/") {
		t.Fatalf("redirect to %q", loc)
	}
	return strings.TrimPrefix(loc, "/p/")
}

// problemDirect creates a problem through the service (for bulk setup).
func problemDirect(t *testing.T, app *testApp, author uuid.UUID, title string, display store.DisplayMode) uuid.UUID {
	t.Helper()
	id, err := app.Svc.CreateProblem(context.Background(), author, service.NewProblem{
		DomainID: 3,
		ProblemFields: service.ProblemFields{
			Title:          title,
			CurrentProcess: "Drivers phone the office with each delivery status and someone types it into a shared sheet.",
			Pain:           "Customers call to ask where things are and nobody knows.",
			Tried:          "A WhatsApp group; it got noisy.",
		},
		Display: display,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func currentRevision(t *testing.T, app *testApp, problemID string) string {
	t.Helper()
	var id uuid.UUID
	if err := app.Store.Pool.QueryRow(context.Background(),
		`SELECT current_revision_id FROM problems WHERE id = $1`, problemID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id.String()
}

func execSQL(t *testing.T, app *testApp, sql string, args ...any) {
	t.Helper()
	if _, err := app.Store.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func mustStatus(t *testing.T, resp *http.Response, want int) string {
	t.Helper()
	b := body(t, resp)
	if resp.StatusCode != want {
		t.Fatalf("%s %s: status %d, want %d: %s", resp.Request.Method, resp.Request.URL.Path, resp.StatusCode, want, b)
	}
	return b
}

var (
	rowIDRe = regexp.MustCompile(`data-problem-id="([0-9a-f-]{36})"`)
	moreRe  = regexp.MustCompile(`hx-get="([^"]*after=[^"]*)"`)
)

// walkList follows a list's keyset pages (first page as a full page, the rest
// as htmx partials) and returns every problem id in order, checking the page
// size on the way.
func walkList(t *testing.T, c *testClient, path string) []string {
	t.Helper()
	ids, _ := walkListHTML(t, c, path)
	return ids
}

// walkListHTML is walkList that also returns every page's HTML, concatenated.
func walkListHTML(t *testing.T, c *testClient, path string) ([]string, string) {
	t.Helper()
	var ids []string
	var all strings.Builder
	b := mustStatus(t, c.get(path), http.StatusOK)
	for pages := 0; ; pages++ {
		if pages > 200 {
			t.Fatal("pagination does not terminate")
		}
		all.WriteString(b)
		rows := rowIDRe.FindAllStringSubmatch(b, -1)
		if pinnedSection := strings.Contains(b, `aria-label="Pinned"`); !pinnedSection && len(rows) > service.ProblemPageSize {
			t.Fatalf("page %d of %s has %d rows", pages, path, len(rows))
		}
		for _, m := range rows {
			ids = append(ids, m[1])
		}
		m := moreRe.FindStringSubmatch(b)
		if m == nil {
			return ids, all.String()
		}
		next := html.UnescapeString(m[1])
		resp := c.getHX(next)
		b = mustStatus(t, resp, http.StatusOK)
		if strings.Contains(b, "<html") {
			t.Fatal("htmx page request returned a full page")
		}
	}
}

func countOf(ids []string, id string) int {
	n := 0
	for _, x := range ids {
		if x == id {
			n++
		}
	}
	return n
}
