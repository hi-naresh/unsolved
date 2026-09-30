package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

func TestAdminMarkInvalidWritesEvent(t *testing.T) {
	ctx := context.Background()
	s := modSvc(t)
	admin, _ := modUser(t, s)
	author, _ := modUser(t, s)
	pid, _ := modProblem(t, s, author, store.DisplayModeAnonymous)

	var ve service.ErrValidation
	if err := s.AdminMarkInvalid(ctx, admin, pid, "  "); !errors.As(err, &ve) {
		t.Fatalf("empty reason: want validation error, got %v", err)
	}
	if err := s.AdminMarkInvalid(ctx, admin, pid, "Duplicate of an older problem"); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminMarkInvalid(ctx, admin, pid, "again"); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("already invalid: want conflict, got %v", err)
	}
	if err := s.AdminMarkInvalid(ctx, admin, uuid.New(), "missing"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("missing problem: want not found, got %v", err)
	}

	var state string
	if err := s.Store.Pool.QueryRow(ctx, `SELECT state FROM problems WHERE id = $1`, pid).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "invalid" {
		t.Fatalf("state = %s", state)
	}
	type ev struct{ from, to, reason string }
	events := func() []ev {
		rows, err := s.Store.Pool.Query(ctx, `SELECT from_state, to_state, reason, actor_id FROM problem_state_events WHERE problem_id = $1 ORDER BY created_at, id`, pid)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []ev
		for rows.Next() {
			var e ev
			var actor uuid.UUID
			if err := rows.Scan(&e.from, &e.to, &e.reason, &actor); err != nil {
				t.Fatal(err)
			}
			if actor != admin {
				t.Fatalf("event actor = %v, want admin", actor)
			}
			out = append(out, e)
		}
		return out
	}
	got := events()
	if len(got) != 1 || got[0] != (ev{"open", "invalid", "Duplicate of an older problem"}) {
		t.Fatalf("events = %+v", got)
	}

	// Reopen: invalid → open, with a second event. Reopening an open problem conflicts.
	if err := s.AdminReopen(ctx, admin, pid, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.AdminReopen(ctx, admin, pid, ""); !errors.Is(err, service.ErrConflict) {
		t.Fatalf("reopen open: want conflict, got %v", err)
	}
	got = events()
	if len(got) != 2 || got[1].from != "invalid" || got[1].to != "open" {
		t.Fatalf("events after reopen = %+v", got)
	}
}

func TestAdminZeroVotesEnqueuesRescore(t *testing.T) {
	ctx := context.Background()
	s := modSvc(t)
	admin, _ := modUser(t, s)
	author, _ := modUser(t, s)
	voter, voterHandle := modUser(t, s)
	other, _ := modUser(t, s)

	p1, r1 := modProblem(t, s, author, store.DisplayModeNamed)
	p2, r2 := modProblem(t, s, author, store.DisplayModeNamed)
	p3, _ := modProblem(t, s, author, store.DisplayModeNamed)
	_, r4 := modProblem(t, s, author, store.DisplayModeNamed) // voted on by someone else only
	sr3 := modSolution(t, s, p3, author)
	sr1 := modSolution(t, s, p1, author)

	for _, rid := range []uuid.UUID{r1, r2} {
		modExec(t, s, `INSERT INTO problem_revision_votes (revision_id, user_id, weight) VALUES ($1, $2, 1)`, rid, voter)
	}
	for _, rid := range []uuid.UUID{sr3, sr1} {
		modExec(t, s, `INSERT INTO solution_revision_votes (revision_id, user_id, weight) VALUES ($1, $2, 1)`, rid, voter)
	}
	modExec(t, s, `INSERT INTO problem_revision_votes (revision_id, user_id, weight) VALUES ($1, $2, 1)`, r4, other)

	n, ids, err := s.AdminZeroVotes(ctx, admin, voterHandle)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("zeroed %d votes, want 4", n)
	}
	want := []uuid.UUID{p1, p2, p3}
	slices.SortFunc(want, modCompareUUID)
	if !slices.Equal(ids, want) {
		t.Fatalf("problem ids = %v, want %v", ids, want)
	}

	var nonZero int
	if err := s.Store.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM problem_revision_votes WHERE user_id = $1 AND weight <> 0) +
		(SELECT count(*) FROM solution_revision_votes WHERE user_id = $1 AND weight <> 0)`, voter).Scan(&nonZero); err != nil {
		t.Fatal(err)
	}
	if nonZero != 0 {
		t.Fatalf("%d votes still weighted", nonZero)
	}
	var otherWeight float32
	if err := s.Store.Pool.QueryRow(ctx, `SELECT weight FROM problem_revision_votes WHERE user_id = $1`, other).Scan(&otherWeight); err != nil {
		t.Fatal(err)
	}
	if otherWeight != 1 {
		t.Fatal("another user's vote was zeroed")
	}

	if !modHasRescoreJob(t, s, want) {
		t.Fatalf("no rescore_all job with problem ids %v", want)
	}

	// Running it again zeroes nothing and enqueues nothing new.
	n, ids, err = s.AdminZeroVotes(ctx, admin, voterHandle)
	if err != nil || n != 0 || len(ids) != 0 {
		t.Fatalf("second run: n=%d ids=%v err=%v", n, ids, err)
	}

	if _, _, err := s.AdminZeroVotes(ctx, admin, "no_such_user"); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("unknown handle: want not found, got %v", err)
	}
}

func TestAdminSuspendAndMeta(t *testing.T) {
	ctx := context.Background()
	s := modSvc(t)
	admin, _ := modUser(t, s)
	u, handle := modUser(t, s)
	suspended := func() bool {
		var b bool
		if err := s.Store.Pool.QueryRow(ctx, `SELECT suspended_at IS NOT NULL FROM users WHERE id = $1`, u).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if err := s.AdminSetSuspended(ctx, admin, handle, true); err != nil || !suspended() {
		t.Fatalf("suspend: err=%v suspended=%v", err, suspended())
	}
	if err := s.AdminSetSuspended(ctx, admin, handle, false); err != nil || suspended() {
		t.Fatalf("unsuspend: err=%v suspended=%v", err, suspended())
	}
	pid, _ := modProblem(t, s, u, store.DisplayModeNamed)
	if on, err := s.AdminToggleMeta(ctx, admin, pid); err != nil || !on {
		t.Fatalf("toggle meta on: %v %v", on, err)
	}
	if on, err := s.AdminToggleMeta(ctx, admin, pid); err != nil || on {
		t.Fatalf("toggle meta off: %v %v", on, err)
	}
}

func TestListOpenReportsPaginates(t *testing.T) {
	ctx := context.Background()
	s := modSvc(t)
	// Resolve everything left by other tests so the page is ours.
	modExec(t, s, `UPDATE reports SET resolved_at = now() WHERE resolved_at IS NULL`)
	reporter, _ := modUser(t, s)
	target, _ := modUser(t, s)
	author, _ := modUser(t, s)
	_, anonRev := modProblem(t, s, author, store.DisplayModeAnonymous)
	if err := s.CreateReport(ctx, reporter, service.ReportProblemRevision, anonRev.String(), "anonymous spam"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < service.AdminPageSize+2; i++ {
		if err := s.CreateReport(ctx, reporter, service.ReportUser, target.String(), "report number x"); err != nil {
			t.Fatal(err)
		}
	}
	page1, next, err := s.ListOpenReports(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != service.AdminPageSize || next == "" {
		t.Fatalf("page1: %d rows, next=%q", len(page1), next)
	}
	first := page1[0]
	if first.TargetKind != service.ReportProblemRevision || !first.TargetAnonymous || first.TargetHandle != "" || first.TargetTitle == "" {
		t.Fatalf("anonymous revision report row leaked or missing data: %+v", first)
	}
	page2, next2, err := s.ListOpenReports(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 3 || next2 != "" {
		t.Fatalf("page2: %d rows, next=%q", len(page2), next2)
	}
	if page2[0].ID == page1[len(page1)-1].ID {
		t.Fatal("pages overlap")
	}
	if err := s.ResolveReport(ctx, reporter, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ResolveReport(ctx, reporter, first.ID); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("resolve twice: want not found, got %v", err)
	}
	if _, _, err := s.ListOpenReports(ctx, "garbage"); err == nil {
		t.Fatal("bad cursor accepted")
	}
}

func modCompareUUID(a, b uuid.UUID) int {
	switch as, bs := a.String(), b.String(); {
	case as < bs:
		return -1
	case as > bs:
		return 1
	}
	return 0
}

// modHasRescoreJob reports whether a rescore_all job with exactly these
// problem ids is in river_job.
func modHasRescoreJob(t *testing.T, s *service.Service, ids []uuid.UUID) bool {
	t.Helper()
	rows, err := s.Store.Pool.Query(context.Background(), `SELECT args FROM river_job WHERE kind = 'rescore_all'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var args struct {
			ProblemIDs []uuid.UUID `json:"problem_ids"`
		}
		if err := json.Unmarshal(raw, &args); err != nil {
			t.Fatal(err)
		}
		got := slices.Clone(args.ProblemIDs)
		slices.SortFunc(got, modCompareUUID)
		if slices.Equal(got, ids) {
			return true
		}
	}
	return false
}
