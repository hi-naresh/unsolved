package service_test

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/service"
)

func TestToggleProblemVote(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, voter := e.user(t), e.user(t)
	pid, rid := e.problem(t, author, "Reconciling cash drawers at close")

	fixed := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	e.svc.Now = func() time.Time { return fixed }
	defer func() { e.svc.Now = func() time.Time { return time.Now().UTC() } }()

	revision := func() (float64, int32) {
		var s float64
		var n int32
		if err := e.st.Pool.QueryRow(ctx, `SELECT score, vote_count FROM problem_revisions WHERE id = $1`, rid).Scan(&s, &n); err != nil {
			t.Fatal(err)
		}
		return s, n
	}
	want := e.svc.Scorer.Contribution(1, fixed)

	steps := []struct {
		voted bool
		count int32
		score float64
	}{{true, 1, want}, {false, 0, 0}, {true, 1, want}}
	for i, st := range steps {
		res, err := e.svc.ToggleProblemVote(ctx, rid, voter)
		if err != nil {
			t.Fatal(err)
		}
		if res.Voted != st.voted || res.Count != st.count || res.ProblemID != pid {
			t.Fatalf("step %d: %+v", i, res)
		}
		score, n := revision()
		if n != st.count || math.Abs(score-st.score) > 1e-9*math.Max(1, want) {
			t.Fatalf("step %d: score %v count %d, want %v %d", i, score, n, st.score, st.count)
		}
	}
	if n := e.jobCount(t, "pick_current_revision", "problem_id", pid.String()); n < 1 {
		t.Fatal("PickCurrentRevision not enqueued")
	}

	// The vote's own weight and time, not the voter's current ones, are
	// subtracted on unvote.
	later := fixed.Add(90 * 24 * time.Hour)
	e.svc.Now = func() time.Time { return later }
	if res, err := e.svc.ToggleProblemVote(ctx, rid, voter); err != nil || res.Voted {
		t.Fatalf("unvote: %+v %v", res, err)
	}
	if score, n := revision(); n != 0 || math.Abs(score) > 1e-9*want {
		t.Fatalf("after late unvote: %v %d", score, n)
	}
}

func TestVoteForbidden(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, voter := e.user(t), e.user(t)
	pid, rid := e.problem(t, author, "Booking delivery slots over the phone")
	sid, err := e.svc.CreateSolution(ctx, pid, author, sampleSolution())
	if err != nil {
		t.Fatal(err)
	}
	srev := solutionRevision(t, e, sid)

	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"self vote on problem revision", func() error { _, err := e.svc.ToggleProblemVote(ctx, rid, author); return err }, service.ErrForbidden},
		{"self vote on solution revision", func() error { _, err := e.svc.ToggleSolutionVote(ctx, srev, author); return err }, service.ErrForbidden},
		{"unknown revision", func() error { _, err := e.svc.ToggleProblemVote(ctx, uuid.New(), voter); return err }, service.ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	if _, err := e.st.Pool.Exec(ctx, `UPDATE problems SET state = 'invalid' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ToggleProblemVote(ctx, rid, voter); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("vote on invalid problem: %v", err)
	}
	if _, err := e.svc.ToggleSolutionVote(ctx, srev, voter); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("solution vote on invalid problem: %v", err)
	}
}

func TestRescoreAll(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author := e.user(t)
	voters := []uuid.UUID{e.user(t), e.user(t), e.user(t), e.user(t)}
	p1, r1 := e.problem(t, author, "Rescore problem number one here")
	p2, r2 := e.problem(t, author, "Rescore problem number two here")
	s1, err := e.svc.CreateSolution(ctx, p1, author, sampleSolution())
	if err != nil {
		t.Fatal(err)
	}
	sr1 := solutionRevision(t, e, s1)

	for i, v := range voters {
		e.svc.Now = func() time.Time { return time.Date(2026, 3, 1+i, 0, 0, 0, 0, time.UTC) }
		for _, r := range []uuid.UUID{r1, r2} {
			if _, err := e.svc.ToggleProblemVote(ctx, r, v); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := e.svc.ToggleSolutionVote(ctx, sr1, v); err != nil {
			t.Fatal(err)
		}
	}
	// One unvote so a delete path is included.
	if _, err := e.svc.ToggleProblemVote(ctx, r1, voters[0]); err != nil {
		t.Fatal(err)
	}
	e.svc.Now = func() time.Time { return time.Now().UTC() }
	for _, p := range []uuid.UUID{p1, p2} {
		if err := jobs.PickCurrentRevision(ctx, e.deps, p); err != nil {
			t.Fatal(err)
		}
	}

	type snap struct {
		score float64
		count int32
	}
	read := func(t *testing.T, table string, id uuid.UUID) snap {
		t.Helper()
		var s snap
		if err := e.st.Pool.QueryRow(ctx, `SELECT score, vote_count FROM `+table+` WHERE id = $1`, id).Scan(&s.score, &s.count); err != nil {
			t.Fatal(err)
		}
		return s
	}
	want := map[uuid.UUID]snap{
		r1: read(t, "problem_revisions", r1), r2: read(t, "problem_revisions", r2), sr1: read(t, "solution_revisions", sr1),
	}
	if want[r1].count != 3 || want[r2].count != 4 || want[sr1].count != 4 {
		t.Fatalf("setup counts: %+v", want)
	}
	corrupt := func(t *testing.T) {
		t.Helper()
		revs, probs := []uuid.UUID{r1, r2, sr1}, []uuid.UUID{p1, p2}
		for _, q := range []struct {
			sql string
			ids []uuid.UUID
		}{
			{`UPDATE problem_revisions SET score = 0, vote_count = 0 WHERE id = ANY($1)`, revs},
			{`UPDATE solution_revisions SET score = 0, vote_count = 0 WHERE id = ANY($1)`, revs},
			{`UPDATE problems SET score = 0 WHERE id = ANY($1)`, probs},
		} {
			if _, err := e.st.Pool.Exec(ctx, q.sql, q.ids); err != nil {
				t.Fatal(err)
			}
		}
	}
	near := func(a, b snap) bool {
		return a.count == b.count && math.Abs(a.score-b.score) <= 1e-9*math.Max(1, math.Abs(b.score))
	}

	t.Run("restricted to one problem", func(t *testing.T) {
		corrupt(t)
		if err := jobs.RescoreAll(ctx, e.deps, e.rc, jobs.RescoreAllArgs{ProblemIDs: []uuid.UUID{p1}}); err != nil {
			t.Fatal(err)
		}
		if got := read(t, "problem_revisions", r1); !near(got, want[r1]) {
			t.Fatalf("r1: %+v want %+v", got, want[r1])
		}
		if got := read(t, "solution_revisions", sr1); !near(got, want[sr1]) {
			t.Fatalf("sr1 (solution of p1): %+v want %+v", got, want[sr1])
		}
		if got := read(t, "problem_revisions", r2); got.count != 0 || got.score != 0 {
			t.Fatalf("r2 outside the restriction was rescored: %+v", got)
		}
		var ps float64
		if err := e.st.Pool.QueryRow(ctx, `SELECT score FROM problems WHERE id = $1`, p1).Scan(&ps); err != nil {
			t.Fatal(err)
		}
		if !near(snap{ps, want[r1].count}, want[r1]) {
			t.Fatalf("problems.score not recomputed: %v", ps)
		}
	})

	t.Run("everything", func(t *testing.T) {
		corrupt(t)
		if err := jobs.RescoreAll(ctx, e.deps, e.rc, jobs.RescoreAllArgs{}); err != nil {
			t.Fatal(err)
		}
		for id, w := range want {
			table := "problem_revisions"
			if id == sr1 {
				table = "solution_revisions"
			}
			if got := read(t, table, id); !near(got, w) {
				t.Fatalf("%s: %+v want %+v", id, got, w)
			}
		}
	})
}
