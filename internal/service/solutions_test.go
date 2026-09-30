package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

func sampleSolution() service.NewSolution {
	return service.NewSolution{
		Kind: store.SolutionKindOffTheShelf,
		Body: "Use the ERP's CSV import: map the spreadsheet columns once, then import every Monday in two minutes.",
	}
}

func solutionRevision(t *testing.T, e *contentEnv, sid uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := e.st.Pool.QueryRow(context.Background(), `SELECT current_revision_id FROM solutions WHERE id = $1`, sid).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCreateSolution(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, helper := e.user(t), e.user(t)
	pid, _ := e.problem(t, author, "Printing pick lists for every order")

	cases := []struct {
		name  string
		edit  func(*service.NewSolution)
		field string
	}{
		{"ok", func(*service.NewSolution) {}, ""},
		{"dont automate", func(s *service.NewSolution) { s.Kind = store.SolutionKindDontAutomate }, ""},
		{"bad kind", func(s *service.NewSolution) { s.Kind = "magic" }, "kind"},
		{"short body", func(s *service.NewSolution) { s.Body = "Use Excel." }, "body"},
		{"long body", func(s *service.NewSolution) { s.Body = strings.Repeat("a", 8001) }, "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := sampleSolution()
			tc.edit(&in)
			sid, err := e.svc.CreateSolution(ctx, pid, helper, in)
			if tc.field != "" {
				wantValidation(t, err, tc.field)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if solutionRevision(t, e, sid) == uuid.Nil {
				t.Fatal("no current revision")
			}
		})
	}

	sid, err := e.svc.CreateSolution(ctx, pid, helper, sampleSolution())
	if err != nil {
		t.Fatal(err)
	}
	root := solutionRevision(t, e, sid)
	body := sampleSolution().Body + " Also keep a mapping sheet."
	if _, err := e.svc.CreateSolutionRevision(ctx, sid, author, service.NewSolutionRevision{ParentRevisionID: root, Body: body}); err == nil {
		t.Fatal("solution revision without why-note accepted")
	} else {
		wantValidation(t, err, "why_note")
	}
	got, err := e.svc.CreateSolutionRevision(ctx, sid, author, service.NewSolutionRevision{ParentRevisionID: root, Body: body, WhyNote: "Added the mapping sheet."})
	if err != nil || got != pid {
		t.Fatalf("solution revision: %v %v", got, err)
	}
}

func TestSoftSolved(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, helper := e.user(t), e.user(t)
	pid, _ := e.problem(t, author, "Keeping allergy info up to date on menus")
	sid, err := e.svc.CreateSolution(ctx, pid, helper, sampleSolution())
	if err != nil {
		t.Fatal(err)
	}
	srev := solutionRevision(t, e, sid)
	softSolved := func() bool {
		var b bool
		if err := e.st.Pool.QueryRow(ctx, `SELECT soft_solved FROM problems WHERE id = $1`, pid).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	var voters []uuid.UUID
	for i := 0; i < 5; i++ {
		v := e.user(t)
		voters = append(voters, v)
		if _, err := e.svc.ToggleSolutionVote(ctx, srev, v); err != nil {
			t.Fatal(err)
		}
		if err := jobs.PickCurrentSolutionRevision(ctx, e.deps, e.rc, sid); err != nil {
			t.Fatal(err)
		}
		if got, want := softSolved(), i == 4; got != want {
			t.Fatalf("after %d votes soft_solved = %v", i+1, got)
		}
	}
	if n := e.jobCount(t, "pick_current_solution_revision", "solution_id", sid.String()); n < 1 {
		t.Fatal("PickCurrentSolutionRevision not enqueued")
	}
	if n := e.jobCount(t, "solved_prompt", "problem_id", pid.String()); n != 1 {
		t.Fatalf("SolvedPrompt enqueued %d times", n)
	}
	var score float64
	_ = e.st.Pool.QueryRow(ctx, `SELECT score FROM solutions WHERE id = $1`, sid).Scan(&score)
	if score <= 0 {
		t.Fatal("solutions.score not set from leader")
	}

	// Unvote → back under the threshold.
	if _, err := e.svc.ToggleSolutionVote(ctx, srev, voters[0]); err != nil {
		t.Fatal(err)
	}
	if err := jobs.PickCurrentSolutionRevision(ctx, e.deps, e.rc, sid); err != nil {
		t.Fatal(err)
	}
	if softSolved() {
		t.Fatal("soft_solved still true after unvote")
	}
}

func TestRecordTrial(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, a, b := e.user(t), e.user(t), e.user(t)
	pid, _ := e.problem(t, author, "Rotating on-call without a spreadsheet")
	sid, err := e.svc.CreateSolution(ctx, pid, author, sampleSolution())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RecordTrial(ctx, sid, a, store.TriedOutcomeFailed, "Our ERP has no import."); err != nil {
		t.Fatal(err)
	}
	// One per user: the second call replaces the first.
	if _, err := e.svc.RecordTrial(ctx, sid, a, store.TriedOutcomeWorked, "Found the import after all."); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RecordTrial(ctx, sid, b, store.TriedOutcomePartly, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RecordTrial(ctx, sid, b, "meh", ""); err == nil {
		t.Fatal("bad outcome accepted")
	} else {
		wantValidation(t, err, "outcome")
	}
	if _, err := e.svc.RecordTrial(ctx, sid, b, store.TriedOutcomeWorked, strings.Repeat("n", 1001)); err == nil {
		t.Fatal("long note accepted")
	}
	pg, err := e.svc.ProblemPage(ctx, pid, &a)
	if err != nil {
		t.Fatal(err)
	}
	if len(pg.Solutions) != 1 {
		t.Fatalf("%d solutions", len(pg.Solutions))
	}
	s := pg.Solutions[0]
	if s.Worked != 1 || s.Partly != 1 || s.Failed != 0 || s.ViewerOutcome != "worked" {
		t.Fatalf("trial counts: %+v", s)
	}
	if len(s.Notes) != 1 || s.Notes[0].Note != "Found the import after all." {
		t.Fatalf("notes: %+v", s.Notes)
	}
	if _, err := e.svc.RecordTrial(ctx, uuid.New(), a, store.TriedOutcomeWorked, ""); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("unknown solution: %v", err)
	}
}
