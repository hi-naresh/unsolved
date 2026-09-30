package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// contentEnv is a Service over the package's real Postgres with an
// insert-only River client, plus the job Deps for running workers inline.
type contentEnv struct {
	svc  *service.Service
	st   *store.Store
	deps jobs.Deps
	rc   *river.Client[pgx.Tx]
}

func newContentEnv(t *testing.T) *contentEnv {
	t.Helper()
	st := storetest.Store(t)
	rc, err := river.NewClient(riverpgxv5.New(st.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{RankHalfLife: 720 * time.Hour, RankMinVotesToTakeOver: 3, SoftSolvedThreshold: 5}
	scorer := ranking.NewDecayScorer(cfg.RankHalfLife)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := service.New(st, rc, scorer, ranking.FlatWeigher{}, cfg, log)
	return &contentEnv{svc: svc, st: st, rc: rc, deps: jobs.Deps{Store: st, Scorer: scorer, Cfg: cfg, Log: log}}
}

var contentUserSeq atomic.Int64

func (e *contentEnv) user(t *testing.T) uuid.UUID {
	t.Helper()
	n := contentUserSeq.Add(1)
	u, _, err := e.svc.SignInWithIdentity(context.Background(), store.ProviderLinkedin,
		fmt.Sprintf("svc-%d-%d", time.Now().UnixNano(), n), "", fmt.Sprintf("Svc User %d", n))
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func sampleProblem(title string) service.NewProblem {
	return service.NewProblem{
		DomainID: 1,
		ProblemFields: service.ProblemFields{
			Title:          title,
			CurrentProcess: "Every Monday we export the orders to a spreadsheet, then re-key them into the ERP by hand.",
			Pain:           "It takes six hours and typos cause wrong shipments.",
			Tried:          "nothing yet",
		},
		Display: store.DisplayModeNamed,
	}
}

func (e *contentEnv) problem(t *testing.T, author uuid.UUID, title string) (problemID, revisionID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	id, err := e.svc.CreateProblem(ctx, author, sampleProblem(title))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.st.GetProblemForWrite(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return id, *p.CurrentRevisionID
}

func (e *contentEnv) jobCount(t *testing.T, kind, key, value string) int {
	t.Helper()
	var n int
	if err := e.st.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM river_job WHERE kind = $1 AND args->>$2 = $3`, kind, key, value).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantValidation(t *testing.T, err error, field string) {
	t.Helper()
	var ve service.ErrValidation
	if !errors.As(err, &ve) {
		t.Fatalf("want ErrValidation(%s), got %v", field, err)
	}
	if _, ok := service.ValidationErrors(err)[field]; !ok {
		t.Fatalf("want ErrValidation on %s, got %v", field, service.ValidationErrors(err))
	}
}

func TestCreateProblem(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author := e.user(t)

	cases := []struct {
		name  string
		edit  func(*service.NewProblem)
		field string // "" = success
	}{
		{"ok", func(*service.NewProblem) {}, ""},
		{"ok anonymous, empty tried", func(p *service.NewProblem) { p.Display = store.DisplayModeAnonymous; p.Tried = "" }, ""},
		{"short title", func(p *service.NewProblem) { p.Title = "too short" }, "title"},
		{"long title", func(p *service.NewProblem) { p.Title = strings.Repeat("x", 141) }, "title"},
		{"short process", func(p *service.NewProblem) { p.CurrentProcess = "we do it by hand" }, "current_process"},
		{"short pain", func(p *service.NewProblem) { p.Pain = "slow" }, "pain"},
		{"long tried", func(p *service.NewProblem) { p.Tried = strings.Repeat("é", 2001) }, "tried"},
		{"no domain", func(p *service.NewProblem) { p.DomainID = 0 }, "domain_id"},
		{"unknown domain", func(p *service.NewProblem) { p.DomainID = 999 }, "domain_id"},
		{"bad display", func(p *service.NewProblem) { p.Display = "loud" }, "display"},
		{"nul byte", func(p *service.NewProblem) { p.Pain = "It takes six hours\x00 and typos cause wrong shipments." }, "pain"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := sampleProblem("Re-keying weekly orders into the ERP " + tc.name)
			tc.edit(&in)
			id, err := e.svc.CreateProblem(ctx, author, in)
			if tc.field != "" {
				wantValidation(t, err, tc.field)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.st.GetProblemForWrite(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if p.CurrentRevisionID == nil {
				t.Fatal("current_revision_id not set")
			}
			var revs int
			var parent *uuid.UUID
			if err := e.st.Pool.QueryRow(ctx, `SELECT count(*), max(parent_revision_id::text)::uuid FROM problem_revisions WHERE problem_id = $1`, id).Scan(&revs, &parent); err != nil {
				t.Fatal(err)
			}
			if revs != 1 || parent != nil {
				t.Fatalf("want one root revision, got %d (parent %v)", revs, parent)
			}
			if n := e.jobCount(t, "embed_revision", "revision_id", p.CurrentRevisionID.String()); n != 1 {
				t.Fatalf("embed job enqueued %d times", n)
			}
		})
	}

	// One transaction: a failure after the problem insert leaves nothing behind.
	var before, after int
	_ = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM problems WHERE author_id = $1`, author).Scan(&before)
	if _, err := e.svc.CreateProblem(ctx, author, func() service.NewProblem {
		p := sampleProblem("Atomic create should roll back")
		p.DomainID = 999
		return p
	}()); err == nil {
		t.Fatal("want error")
	}
	_ = e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM problems WHERE author_id = $1`, author).Scan(&after)
	if before != after {
		t.Fatalf("failed create left rows: %d → %d", before, after)
	}
}

func TestForkCopiesTextNotVotes(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, voter, forker := e.user(t), e.user(t), e.user(t)
	pid, rid := e.problem(t, author, "Scheduling night shifts on a whiteboard")
	if _, err := e.svc.ToggleProblemVote(ctx, rid, voter); err != nil {
		t.Fatal(err)
	}

	fid, err := e.svc.Fork(ctx, pid, rid, forker, store.DisplayModeAnonymous)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := e.svc.ProblemPage(ctx, fid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Problem.Title != "Scheduling night shifts on a whiteboard" || pg.Problem.VoteCount != 0 || pg.Problem.Score != 0 {
		t.Fatalf("fork: %+v", pg.Problem)
	}
	if pg.Problem.ForkedFromProblemID == nil || *pg.Problem.ForkedFromProblemID != pid {
		t.Fatal("fork does not link back")
	}
	if pg.Problem.PosterDisplay != store.DisplayModeAnonymous {
		t.Fatal("fork display mode not kept")
	}
	orig, err := e.svc.ProblemPage(ctx, pid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(orig.Forks) != 1 || orig.Forks[0].ID != fid {
		t.Fatalf("original does not list the fork: %+v", orig.Forks)
	}

	// Revision of another problem → not found.
	_, otherRev := e.problem(t, author, "Another problem to fork from")
	if _, err := e.svc.Fork(ctx, pid, otherRev, forker, ""); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("fork with foreign revision: %v", err)
	}
}

func TestFrontPageSeedPinningAndPaging(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author := e.user(t)
	seed, _ := e.problem(t, author, "Seed problem pinned on the front page")
	if _, err := e.st.Pool.Exec(ctx, `UPDATE problems SET is_seed = true WHERE id = $1`, seed); err != nil {
		t.Fatal(err)
	}
	var nonSeed int
	if err := e.st.Pool.QueryRow(ctx, `SELECT count(*) FROM problems WHERE NOT is_seed`).Scan(&nonSeed); err != nil {
		t.Fatal(err)
	}
	if nonSeed < 50 {
		fp, err := e.svc.FrontPage(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		if !containsProblem(fp.Pinned, seed) || containsProblem(fp.Items, seed) {
			t.Fatal("seed not pinned while < 50 non-seed problems")
		}
	}
	for i := nonSeed; i < 50; i++ {
		e.problem(t, author, fmt.Sprintf("Filler problem number %d for pinning", i))
	}
	// ≥ 50 non-seed: no pinning; walk every page and find each problem once.
	seen := map[uuid.UUID]int{}
	after := ""
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("pagination does not terminate")
		}
		fp, err := e.svc.FrontPage(ctx, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(fp.Pinned) != 0 {
			t.Fatal("seeds still pinned at ≥ 50 non-seed problems")
		}
		if len(fp.Items) > service.ProblemPageSize {
			t.Fatalf("page of %d", len(fp.Items))
		}
		for _, it := range fp.Items {
			seen[it.ID]++
		}
		if fp.Next == "" {
			break
		}
		after = fp.Next
	}
	if seen[seed] != 1 {
		t.Fatalf("seed seen %d times", seen[seed])
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("problem %s listed %d times", id, n)
		}
	}
	if _, err := e.svc.FrontPage(ctx, "garbage"); err == nil {
		t.Fatal("bad cursor accepted")
	}
}

func containsProblem(items []service.ProblemListItem, id uuid.UUID) bool {
	for _, it := range items {
		if it.ID == id {
			return true
		}
	}
	return false
}
