package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/service"
)

func revisionInput(parent uuid.UUID, why string) service.NewRevision {
	p := sampleProblem("Re-keying weekly orders, clarified version")
	return service.NewRevision{ParentRevisionID: parent, ProblemFields: p.ProblemFields, WhyNote: why}
}

func TestCreateRevision(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, editor := e.user(t), e.user(t)
	pid, root := e.problem(t, author, "Re-keying weekly orders into the ERP")
	_, foreign := e.problem(t, author, "A different problem entirely")

	cases := []struct {
		name   string
		in     service.NewRevision
		field  string
		target error
	}{
		{"ok", revisionInput(root, "Clarified the title."), "", nil},
		{"missing why-note", revisionInput(root, "   "), "why_note", nil},
		{"why-note too long", revisionInput(root, strings.Repeat("w", 501)), "why_note", nil},
		{"parent from another problem", revisionInput(foreign, "Moved text."), "parent_revision_id", nil},
		{"unknown parent", revisionInput(uuid.New(), "Moved text."), "parent_revision_id", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := e.svc.CreateRevision(ctx, pid, editor, tc.in)
			if tc.field != "" {
				wantValidation(t, err, tc.field)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if n := e.jobCount(t, "embed_revision", "revision_id", id.String()); n != 1 {
				t.Fatalf("embed job enqueued %d times", n)
			}
			// A new revision never becomes current by itself.
			p, _ := e.st.GetProblemForWrite(ctx, pid)
			if *p.CurrentRevisionID != root {
				t.Fatal("new revision became current without votes")
			}
		})
	}

	// Invalid problems take no revisions.
	if _, err := e.st.Pool.Exec(ctx, `UPDATE problems SET state = 'invalid' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateRevision(ctx, pid, editor, revisionInput(root, "Late edit.")); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("revision on invalid problem: %v", err)
	}
}

func TestEvolutionTree(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	a, b := e.user(t), e.user(t)
	pid, root := e.problem(t, a, "Tracking loaner laptops in a shared inbox")
	c1, err := e.svc.CreateRevision(ctx, pid, b, revisionInput(root, "first child"))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := e.svc.CreateRevision(ctx, pid, b, revisionInput(root, "second child"))
	if err != nil {
		t.Fatal(err)
	}
	g1, err := e.svc.CreateRevision(ctx, pid, a, revisionInput(c1, "grandchild"))
	if err != nil {
		t.Fatal(err)
	}
	ev, err := e.svc.Evolution(ctx, pid, &b)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		id    uuid.UUID
		depth int
	}{{root, 0}, {c1, 1}, {g1, 2}, {c2, 1}}
	if len(ev.Revisions) != len(want) {
		t.Fatalf("got %d revisions", len(ev.Revisions))
	}
	for i, w := range want {
		got := ev.Revisions[i]
		if got.ID != w.id || got.Depth != w.depth {
			t.Fatalf("node %d: got %s depth %d, want %s depth %d", i, got.ID, got.Depth, w.id, w.depth)
		}
	}
	if !ev.Revisions[0].IsCurrent || ev.Revisions[1].IsCurrent {
		t.Fatal("current badge wrong")
	}
	if !ev.Revisions[1].ViewerIsAuthor || ev.Revisions[0].ViewerIsAuthor {
		t.Fatal("viewer_is_author wrong")
	}
}

func TestPickCurrentRevision(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	author, editor := e.user(t), e.user(t)
	voters := []uuid.UUID{e.user(t), e.user(t), e.user(t)}

	current := func(pid uuid.UUID) (uuid.UUID, float64) {
		t.Helper()
		var id uuid.UUID
		var score float64
		if err := e.st.Pool.QueryRow(ctx, `SELECT current_revision_id, score FROM problems WHERE id = $1`, pid).Scan(&id, &score); err != nil {
			t.Fatal(err)
		}
		return id, score
	}

	t.Run("takeover blocked under 3 votes, happens at 3", func(t *testing.T) {
		pid, root := e.problem(t, author, "Matching invoices to purchase orders")
		rev, err := e.svc.CreateRevision(ctx, pid, editor, revisionInput(root, "Sharper description."))
		if err != nil {
			t.Fatal(err)
		}
		for i, v := range voters {
			if _, err := e.svc.ToggleProblemVote(ctx, rev, v); err != nil {
				t.Fatal(err)
			}
			if err := jobs.PickCurrentRevision(ctx, e.deps, pid); err != nil {
				t.Fatal(err)
			}
			cur, score := current(pid)
			switch {
			case i < 2 && cur != root:
				t.Fatalf("takeover with %d votes", i+1)
			case i == 2 && cur != rev:
				t.Fatal("no takeover at 3 votes")
			case i == 2:
				var rs float64
				_ = e.st.Pool.QueryRow(ctx, `SELECT score FROM problem_revisions WHERE id = $1`, rev).Scan(&rs)
				if score != rs {
					t.Fatalf("problems.score %v != leader score %v", score, rs)
				}
			}
		}
		// Idempotent.
		if err := jobs.PickCurrentRevision(ctx, e.deps, pid); err != nil {
			t.Fatal(err)
		}
		if cur, _ := current(pid); cur != rev {
			t.Fatal("second run changed the leader")
		}
	})

	t.Run("tie keeps incumbent", func(t *testing.T) {
		pid, root := e.problem(t, author, "Counting stock in the walk-in freezer")
		rev, err := e.svc.CreateRevision(ctx, pid, editor, revisionInput(root, "Tie test."))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.st.Pool.Exec(ctx, `UPDATE problem_revisions SET score = 42, vote_count = 10 WHERE problem_id = $1`, pid); err != nil {
			t.Fatal(err)
		}
		if err := jobs.PickCurrentRevision(ctx, e.deps, pid); err != nil {
			t.Fatal(err)
		}
		if cur, score := current(pid); cur != root || score != 42 {
			t.Fatalf("tie: current %s score %v", cur, score)
		}
		_ = rev
	})
}

func TestProblemRevisionReadForRevise(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	a := e.user(t)
	pid, root := e.problem(t, a, "Chasing timesheets every Friday afternoon")
	src, err := e.svc.ReviseSource(ctx, pid, nil)
	if err != nil || src.RevisionID != root || !src.IsCurrent {
		t.Fatalf("revise source: %+v %v", src, err)
	}
	_, other := e.problem(t, a, "Some other problem for revise")
	if _, err := e.svc.ReviseSource(ctx, pid, &other); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("foreign from: %v", err)
	}
}
