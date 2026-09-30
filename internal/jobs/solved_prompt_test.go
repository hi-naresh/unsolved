package jobs

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

func TestOutcomeToken(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	claims := OutcomeClaims{
		ProblemID: uuid.Must(uuid.NewV7()), SolutionID: uuid.Must(uuid.NewV7()), PosterID: uuid.Must(uuid.NewV7()),
		Expires: now.Add(OutcomeTokenTTL),
	}
	good := MakeOutcomeToken(key, claims)
	expired := MakeOutcomeToken(key, OutcomeClaims{ProblemID: claims.ProblemID, SolutionID: claims.SolutionID, PosterID: claims.PosterID, Expires: now.Add(-time.Second)})

	// flip changes one byte of the decoded token at i and re-encodes it.
	flip := func(tok string, i int) string {
		b, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil {
			t.Fatal(err)
		}
		b[i] ^= 0x01
		return base64.RawURLEncoding.EncodeToString(b)
	}

	cases := []struct {
		name  string
		key   []byte
		token string
		now   time.Time
		want  error
	}{
		{"round trip", key, good, now, nil},
		{"just before expiry", key, good, claims.Expires.Add(-time.Second), nil},
		{"at expiry", key, good, claims.Expires, ErrOutcomeTokenExpired},
		{"after 14 days", key, good, now.Add(OutcomeTokenTTL + time.Hour), ErrOutcomeTokenExpired},
		{"expired token", key, expired, now, ErrOutcomeTokenExpired},
		{"tampered version", key, flip(good, 0), now, ErrOutcomeTokenInvalid},
		{"tampered problem", key, flip(good, 1), now, ErrOutcomeTokenInvalid},
		{"tampered solution", key, flip(good, 20), now, ErrOutcomeTokenInvalid},
		{"tampered poster", key, flip(good, 40), now, ErrOutcomeTokenInvalid},
		{"tampered expiry", key, flip(good, 56), now, ErrOutcomeTokenInvalid},
		{"tampered mac", key, flip(good, outcomeTokenLen-1), now, ErrOutcomeTokenInvalid},
		{"expired and tampered", key, flip(expired, 56), now, ErrOutcomeTokenInvalid},
		{"other key", []byte("another key"), good, now, ErrOutcomeTokenInvalid},
		{"truncated", key, good[:len(good)-4], now, ErrOutcomeTokenInvalid},
		{"extended", key, good + "AAAA", now, ErrOutcomeTokenInvalid},
		{"not base64", key, "not a token!", now, ErrOutcomeTokenInvalid},
		{"empty", key, "", now, ErrOutcomeTokenInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOutcomeToken(tc.key, tc.token, tc.now)
			if err != tc.want {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == nil && got != claims {
				t.Fatalf("claims = %+v, want %+v", got, claims)
			}
		})
	}
	if strings.ContainsAny(good, "/+=") {
		t.Fatalf("token is not URL-path safe: %q", good)
	}
}

// ---- worker ------------------------------------------------------------------

// fakePostmark records every request it receives and answers with status.
type fakePostmark struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	reqs   []postmarkReq
}

type postmarkReq struct {
	Path   string
	Header http.Header
	Email  PostmarkEmail
}

func newFakePostmark(t *testing.T, status int) *fakePostmark {
	t.Helper()
	f := &fakePostmark{status: status}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var e PostmarkEmail
		_ = json.Unmarshal(b, &e)
		f.mu.Lock()
		f.reqs = append(f.reqs, postmarkReq{Path: r.URL.Path, Header: r.Header.Clone(), Email: e})
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		if f.status == http.StatusOK {
			_, _ = w.Write([]byte(`{"ErrorCode":0,"Message":"OK","MessageID":"abc"}`))
		} else {
			_, _ = w.Write([]byte(`{"ErrorCode":406,"Message":"You tried to send to recipient(s) that have been marked as inactive."}`))
		}
	}))
	t.Cleanup(f.Close)
	restore := SetPostmarkBaseURL(f.URL)
	t.Cleanup(restore)
	return f
}

func (f *fakePostmark) requests() []postmarkReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]postmarkReq(nil), f.reqs...)
}

const promptSolutionBody = "Use the courier's tracking webhook and post each status into the shared sheet automatically."

// promptFixture is a soft-solved open problem whose poster has an email, and
// its top solution.
type promptFixture struct {
	ProblemID, SolutionID, PosterID uuid.UUID
}

func newPromptFixture(t *testing.T, pool *pgxpool.Pool, email string) promptFixture {
	t.Helper()
	ctx := context.Background()
	pid, _ := insertProblem(t, pool, "Chasing delivery status by phone", testProcess, testPain)
	var f promptFixture
	f.ProblemID = pid
	if err := pool.QueryRow(ctx, `SELECT author_id FROM problems WHERE id = $1`, pid).Scan(&f.PosterID); err != nil {
		t.Fatal(err)
	}
	helper := uuid.Must(uuid.NewV7())
	insertUser(t, pool, helper)
	f.SolutionID = uuid.Must(uuid.NewV7())
	srid := uuid.Must(uuid.NewV7())
	if _, err := pool.Exec(ctx, `
		WITH s AS (
		  INSERT INTO solutions (id, problem_id, author_id, author_display, kind, current_revision_id)
		  VALUES ($1, $2, $3, 'named', 'off_the_shelf', $4)
		)
		INSERT INTO solution_revisions (id, solution_id, author_id, author_display, body, vote_count)
		VALUES ($4, $1, $3, 'named', $5, 5)`, f.SolutionID, pid, helper, srid, promptSolutionBody); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE problems SET soft_solved = true WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	if email != "" {
		if _, err := pool.Exec(ctx, `UPDATE users SET email = $2 WHERE id = $1`, f.PosterID, email); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func promptDeps(st *store.Store, token string) Deps {
	return Deps{
		Store: st, Log: testLog(),
		Cfg: config.Config{BaseURL: "https://unsolved.example:8443", PostmarkToken: token, DatabaseURL: "postgres://x"},
	}
}

func TestSolvedPromptSkips(t *testing.T) {
	st := storetest.Store(t)
	pool := st.Pool
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		email string
		token string
		setup func(f promptFixture)
	}{
		{"no email", "", "pm-token", nil},
		{"blank email", "  ", "pm-token", nil},
		{"postmark disabled", "poster@example.com", "", nil},
		{"poster deleted", "poster@example.com", "pm-token", func(f promptFixture) {
			exec(`UPDATE users SET deleted_at = now() WHERE id = $1`, f.PosterID)
		}},
		{"poster suspended", "poster@example.com", "pm-token", func(f promptFixture) {
			exec(`UPDATE users SET suspended_at = now() WHERE id = $1`, f.PosterID)
		}},
		{"problem solved", "poster@example.com", "pm-token", func(f promptFixture) {
			exec(`UPDATE problems SET state = 'solved' WHERE id = $1`, f.ProblemID)
		}},
		{"problem invalid", "poster@example.com", "pm-token", func(f promptFixture) {
			exec(`UPDATE problems SET state = 'invalid' WHERE id = $1`, f.ProblemID)
		}},
		{"no longer soft-solved", "poster@example.com", "pm-token", func(f promptFixture) {
			exec(`UPDATE problems SET soft_solved = false WHERE id = $1`, f.ProblemID)
		}},
		{"poster already tried it", "poster@example.com", "pm-token", func(f promptFixture) {
			exec(`INSERT INTO solution_trials (solution_id, user_id, outcome) VALUES ($1, $2, 'partly')`, f.SolutionID, f.PosterID)
		}},
		{"solution of another problem", "poster@example.com", "pm-token", func(f promptFixture) {
			other, _ := insertProblem(t, pool, "Another problem entirely here", testProcess, testPain)
			exec(`UPDATE solutions SET problem_id = $2 WHERE id = $1`, f.SolutionID, other)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pm := newFakePostmark(t, http.StatusOK)
			f := newPromptFixture(t, pool, tc.email)
			if tc.setup != nil {
				tc.setup(f)
			}
			w := &SolvedPromptWorker{d: promptDeps(st, tc.token)}
			if err := w.Work(ctx, &river.Job[SolvedPromptArgs]{Args: SolvedPromptArgs{ProblemID: f.ProblemID, SolutionID: f.SolutionID}}); err != nil {
				t.Fatal(err)
			}
			if n := len(pm.requests()); n != 0 {
				t.Fatalf("sent %d emails, want none", n)
			}
		})
	}
	t.Run("missing problem", func(t *testing.T) {
		pm := newFakePostmark(t, http.StatusOK)
		w := &SolvedPromptWorker{d: promptDeps(st, "pm-token")}
		if err := w.Work(ctx, &river.Job[SolvedPromptArgs]{Args: SolvedPromptArgs{ProblemID: uuid.New(), SolutionID: uuid.New()}}); err != nil {
			t.Fatal(err)
		}
		if len(pm.requests()) != 0 {
			t.Fatal("sent an email for a missing problem")
		}
	})
}

var linkRe = regexp.MustCompile(`https://unsolved\.example:8443/solved/([A-Za-z0-9_-]+)\?outcome=(worked|partly|failed)`)

func TestSolvedPromptSends(t *testing.T) {
	st := storetest.Store(t)
	ctx := context.Background()
	pm := newFakePostmark(t, http.StatusOK)
	f := newPromptFixture(t, st.Pool, "poster@example.com")
	d := promptDeps(st, "pm-token")
	now := time.Now().UTC()
	d.Now = func() time.Time { return now }
	w := &SolvedPromptWorker{d: d}
	if err := w.Work(ctx, &river.Job[SolvedPromptArgs]{Args: SolvedPromptArgs{ProblemID: f.ProblemID, SolutionID: f.SolutionID}}); err != nil {
		t.Fatal(err)
	}
	reqs := pm.requests()
	if len(reqs) != 1 {
		t.Fatalf("sent %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.Path != "/email" || r.Header.Get("X-Postmark-Server-Token") != "pm-token" ||
		r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Accept") != "application/json" {
		t.Fatalf("bad request: path %q headers %v", r.Path, r.Header)
	}
	e := r.Email
	if e.From != "no-reply@unsolved.example" || e.To != "poster@example.com" || e.MessageStream != "outbound" {
		t.Fatalf("bad envelope: %+v", e)
	}
	if !strings.Contains(e.Subject, `"Chasing delivery status by phone"`) {
		t.Fatalf("subject %q", e.Subject)
	}
	for _, body := range []string{e.TextBody, e.HtmlBody} {
		if !strings.Contains(body, "enough votes to count as solved") || !strings.Contains(body, "Did it work for you?") ||
			!strings.Contains(body, "tracking webhook and post each status") ||
			!strings.Contains(body, "Use an existing tool") {
			t.Fatalf("body lacks the prompt or the solution:\n%s", body)
		}
		links := linkRe.FindAllStringSubmatch(body, -1)
		if len(links) != 3 || links[0][2] != "worked" || links[1][2] != "partly" || links[2][2] != "failed" {
			t.Fatalf("want worked/partly/failed links, got %v", links)
		}
		c, err := ParseOutcomeToken(d.Cfg.SigningKey(OutcomeTokenPurpose), links[0][1], now)
		if err != nil {
			t.Fatal(err)
		}
		want := OutcomeClaims{ProblemID: f.ProblemID, SolutionID: f.SolutionID, PosterID: f.PosterID, Expires: time.Unix(now.Add(OutcomeTokenTTL).Unix(), 0).UTC()}
		if c != want {
			t.Fatalf("claims %+v, want %+v", c, want)
		}
	}
	if strings.Contains(e.HtmlBody, "<script") {
		t.Fatal("unescaped HTML")
	}
}

func TestSolvedPromptPostmarkErrors(t *testing.T) {
	st := storetest.Store(t)
	ctx := context.Background()
	f := newPromptFixture(t, st.Pool, "poster@example.com")
	job := &river.Job[SolvedPromptArgs]{Args: SolvedPromptArgs{ProblemID: f.ProblemID, SolutionID: f.SolutionID}}
	w := &SolvedPromptWorker{d: promptDeps(st, "pm-token")}

	newFakePostmark(t, http.StatusUnprocessableEntity)
	if err := w.Work(ctx, job); err != nil {
		t.Fatalf("422 should not be retried: %v", err)
	}
	newFakePostmark(t, http.StatusInternalServerError)
	err := w.Work(ctx, job)
	if err == nil {
		t.Fatal("500 should be retried")
	}
	if strings.Contains(err.Error(), "recipient") {
		t.Fatalf("error text carries Postmark's message (may quote the address): %v", err)
	}
}

func TestSenderAndExcerpt(t *testing.T) {
	for in, want := range map[string]string{
		"https://unsolved.work":      "no-reply@unsolved.work",
		"http://localhost:8080":      "no-reply@localhost",
		"https://www.unsolved.work/": "no-reply@www.unsolved.work",
	} {
		if got, err := senderAddress(in); err != nil || got != want {
			t.Errorf("senderAddress(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := senderAddress(""); err == nil {
		t.Error("empty BASE_URL accepted")
	}
	if got := excerpt("a  b\n\nc", 10); got != "a b c" {
		t.Errorf("excerpt collapse: %q", got)
	}
	if got := excerpt(strings.Repeat("é", 20), 10); got != strings.Repeat("é", 9)+"…" {
		t.Errorf("excerpt cut: %q", got)
	}
}
