package http

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
)

// discVec: normalised bag of hashed words, so texts sharing words are close.
func discVec(text string) []float32 {
	v := make([]float32, jobs.EmbeddingDim)
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%jobs.EmbeddingDim]++
	}
	var n float64
	for _, f := range v {
		n += float64(f * f)
	}
	if n == 0 {
		v[0], n = 1, 1
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(n))
	}
	return v
}

// discML is a fake ML service; delay > 1.5 s simulates a hung service.
func discML(t *testing.T, delay time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		var in struct{ Texts []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := make([][]float32, len(in.Texts))
		for i, s := range in.Texts {
			out[i] = discVec(s)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"vectors": out})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// discProblem inserts a problem + first revision by u with raw SQL (test
// setup only; the posting flow belongs to another phase). embed=true stores
// the fake embedding directly.
func (a *testApp) discProblem(t *testing.T, u store.User, display, title, process, pain string, embed bool) (pid, rid uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	pid, rid = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	_, err := a.Store.Pool.Exec(ctx, `
		WITH p AS (
		  INSERT INTO problems (id, domain_id, author_id, author_display, current_revision_id)
		  VALUES ($1, 3, $2, $7, $3)
		)
		INSERT INTO problem_revisions (id, problem_id, author_id, author_display, title, current_process, pain, tried)
		VALUES ($3, $1, $2, $7, $4, $5, $6, 'nothing yet')`,
		pid, u.ID, rid, title, process, pain, display)
	if err != nil {
		t.Fatal(err)
	}
	if embed {
		if err := a.Store.SetRevisionEmbedding(ctx, store.SetRevisionEmbeddingParams{
			ID: rid, Embedding: jobs.VectorLiteral(discVec(jobs.RevisionText(title, process, pain))),
		}); err != nil {
			t.Fatal(err)
		}
	}
	return pid, rid
}

const (
	discTitle   = "Stocktake counts copied from clipboards"
	discProcess = "Every Sunday staff count shelves onto paper clipboards and a manager retypes the stocktake counts into the till system."
	discPain    = "Retyping takes four hours and typos leave the stock levels wrong all week."
)

func TestDiscoverySearchPage(t *testing.T) {
	ml := discML(t, 0)
	app := newTestApp(t, func(c *config.Config) { c.MLURL = ml.URL })
	author := app.newUser(t)
	pid, _ := app.discProblem(t, author, "anonymous", discTitle, discProcess, discPain, true)
	c := app.anon(t)

	resp := c.get("/search?q=" + url.QueryEscape("stocktake clipboards retyped into the till"))
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(got, discTitle) || !strings.Contains(got, "/p/"+pid.String()) {
		t.Fatalf("search: %d\n%s", resp.StatusCode, got)
	}
	if strings.Contains(got, author.Handle) || !strings.Contains(got, "Anonymous") {
		t.Fatal("anonymous author's handle rendered in search results")
	}
	if !strings.Contains(got, "<html") {
		t.Fatal("full page expected")
	}

	resp = c.getHX("/search?q=stocktake")
	got = body(t, resp)
	if resp.StatusCode != http.StatusOK || strings.Contains(got, "<html") || !strings.Contains(got, `id="search-results"`) {
		t.Fatalf("htmx partial: %d\n%s", resp.StatusCode, got)
	}

	// Empty q: trending from trending_problems.
	ctx := context.Background()
	if _, err := app.Store.Pool.Exec(ctx, `DELETE FROM trending_problems`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Store.Pool.Exec(ctx, `INSERT INTO trending_problems (problem_id, cluster_id, rank) VALUES ($1, 1, 1)`, pid); err != nil {
		t.Fatal(err)
	}
	resp = c.get("/search")
	got = body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(got, "Trending this week") || !strings.Contains(got, discTitle) {
		t.Fatalf("trending: %d\n%s", resp.StatusCode, got)
	}
}

func TestDiscoverySearchWithoutML(t *testing.T) {
	slow := discML(t, 3*time.Second)
	for name, mlURL := range map[string]string{"disabled": "", "timeout": slow.URL} {
		t.Run(name, func(t *testing.T) {
			app := newTestApp(t, func(c *config.Config) { c.MLURL = mlURL })
			c := app.anon(t)
			start := time.Now()
			resp := c.get("/search?q=stocktake")
			got := body(t, resp)
			if resp.StatusCode != http.StatusOK || !strings.Contains(got, "Search is unavailable right now") || !strings.Contains(got, "Trending this week") {
				t.Fatalf("want friendly fallback, got %d\n%s", resp.StatusCode, got)
			}
			if d := time.Since(start); d > 2500*time.Millisecond {
				t.Fatalf("search took %v with ML hung", d)
			}
		})
	}
}

func TestDiscoverySimilar(t *testing.T) {
	ml := discML(t, 0)
	app := newTestApp(t, func(c *config.Config) { c.MLURL = ml.URL })
	author := app.newUser(t)
	pid, _ := app.discProblem(t, author, "named", discTitle, discProcess, discPain, true)
	form := url.Values{
		"title":           {"Stocktake counts retyped from paper"},
		"current_process": {discProcess},
		"pain":            {discPain},
		"domain":          {"retail"},
	}

	// Signed out: the duplicate check needs an account.
	resp := app.anon(t).postHX("/new/similar", form)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon: %d", resp.StatusCode)
	}

	c := app.as(t, app.newUser(t))
	resp = c.postHX("/new/similar", form)
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(got, "These look similar") || !strings.Contains(got, "/p/"+pid.String()) {
		t.Fatalf("similar: %d\n%s", resp.StatusCode, got)
	}

	// Nothing close → empty body.
	resp = c.postHX("/new/similar", url.Values{
		"title":           {"Choir sheet music photocopies"},
		"current_process": {"The conductor photocopies all sheet music for every singer before each rehearsal."},
		"pain":            {"Paper everywhere."},
	})
	if got := body(t, resp); resp.StatusCode != http.StatusOK || strings.TrimSpace(got) != "" {
		t.Fatalf("unrelated: %d %q", resp.StatusCode, got)
	}
}

func TestDiscoverySimilarFailsOpen(t *testing.T) {
	slow := discML(t, 3*time.Second)
	app := newTestApp(t, func(c *config.Config) { c.MLURL = slow.URL })
	c := app.as(t, app.newUser(t))
	start := time.Now()
	resp := c.postHX("/new/similar", url.Values{"title": {discTitle}, "current_process": {discProcess}, "pain": {discPain}})
	if got := body(t, resp); resp.StatusCode != http.StatusOK || strings.TrimSpace(got) != "" {
		t.Fatalf("want empty 200, got %d %q", resp.StatusCode, got)
	}
	if d := time.Since(start); d > 2500*time.Millisecond {
		t.Fatalf("took %v", d)
	}
}

func TestDiscoveryEmbedJobThroughRiver(t *testing.T) {
	ml := discML(t, 0)
	app := newTestApp(t, func(c *config.Config) { c.MLURL = ml.URL })
	_, rid := app.discProblem(t, app.newUser(t), "named", discTitle, discProcess, discPain, false)
	if _, err := app.River.Insert(context.Background(), jobs.EmbedRevisionArgs{RevisionID: rid}, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, 10*time.Second, func() bool {
		var ok bool
		_ = app.Store.Pool.QueryRow(context.Background(),
			`SELECT embedding IS NOT NULL FROM problem_revisions WHERE id = $1`, rid).Scan(&ok)
		return ok
	})
}

func TestDiscoveryAdminMerges(t *testing.T) {
	adminHandle := fmt.Sprintf("disc_admin_%d", time.Now().UnixNano()%1_000_000)
	app := newTestApp(t, func(c *config.Config) { c.AdminHandles = append(c.AdminHandles, adminHandle) })
	ctx := context.Background()
	admin := app.newUser(t)
	if _, err := app.Store.Pool.Exec(ctx, `UPDATE users SET handle = $1 WHERE id = $2`, adminHandle, admin.ID); err != nil {
		t.Fatal(err)
	}
	author := app.newUser(t)
	p1, _ := app.discProblem(t, author, "named", "Merge pair first problem title", discProcess, discPain, true)
	p2, _ := app.discProblem(t, author, "named", "Merge pair second problem title", discProcess, discPain, true)
	a, b := p1, p2
	if a.String() > b.String() {
		a, b = b, a
	}
	mid := uuid.Must(uuid.NewV7())
	if _, err := app.Store.Pool.Exec(ctx,
		`INSERT INTO merge_suggestions (id, problem_a_id, problem_b_id, similarity) VALUES ($1, $2, $3, 0.93)`, mid, a, b); err != nil {
		t.Fatal(err)
	}

	// Non-admins get a 404.
	resp := app.as(t, app.newUser(t)).get("/admin/merges")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("non-admin: %d", resp.StatusCode)
	}

	c := app.as(t, admin)
	resp = c.get("/admin/merges")
	got := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(got, "Merge pair first problem title") ||
		!strings.Contains(got, "Merge pair second problem title") || !strings.Contains(got, "93% similar") {
		t.Fatalf("admin merges: %d\n%s", resp.StatusCode, got)
	}

	resp = c.postHX("/admin/merges/"+mid.String()+"/dismiss", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dismiss: %d", resp.StatusCode)
	}
	var dismissed bool
	_ = app.Store.Pool.QueryRow(ctx, `SELECT dismissed_at IS NOT NULL FROM merge_suggestions WHERE id = $1`, mid).Scan(&dismissed)
	if !dismissed {
		t.Fatal("not dismissed")
	}
	if got := body(t, c.get("/admin/merges")); strings.Contains(got, "Merge pair first problem title") {
		t.Fatal("dismissed suggestion still listed")
	}
	resp = c.postForm("/admin/merges/"+uuid.Must(uuid.NewV7()).String()+"/dismiss", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id: %d", resp.StatusCode)
	}
}
