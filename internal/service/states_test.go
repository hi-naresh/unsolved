package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

func TestSetProblemState(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	poster, other := e.user(t), e.user(t)
	pid, _ := e.problem(t, poster, "Tracking which forklift needs service")

	steps := []struct {
		name  string
		actor string
		to    store.ProblemState
		want  error
	}{
		{"non-poster forbidden", "other", store.ProblemStateSolved, service.ErrForbidden},
		{"poster cannot mark invalid", "poster", store.ProblemStateInvalid, service.ErrValidation{}},
		{"reopen an open problem", "poster", store.ProblemStateOpen, service.ErrConflict},
		{"poster marks solved", "poster", store.ProblemStateSolved, nil},
		{"poster reopens", "poster", store.ProblemStateOpen, nil},
		{"poster marks solved again", "poster", store.ProblemStateSolved, nil},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			actor := poster
			if st.actor == "other" {
				actor = other
			}
			err := e.svc.SetProblemState(ctx, pid, actor, st.to, "")
			switch want := st.want.(type) {
			case nil:
				if err != nil {
					t.Fatal(err)
				}
			case service.ErrValidation:
				var ve service.ErrValidation
				if !errors.As(err, &ve) {
					t.Fatalf("want validation error, got %v", err)
				}
			default:
				if !errors.Is(err, want) {
					t.Fatalf("got %v, want %v", err, want)
				}
			}
		})
	}
	pg, err := e.svc.ProblemPage(ctx, pid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Problem.State != store.ProblemStateSolved {
		t.Fatalf("state %s", pg.Problem.State)
	}
	if len(pg.History) != 3 {
		t.Fatalf("want 3 state events, got %d", len(pg.History))
	}
	h := pg.History
	if h[0].FromState != "open" || h[0].ToState != "solved" || h[1].ToState != "open" || h[2].ToState != "solved" || h[0].ActorKind != "poster" {
		t.Fatalf("history: %+v", h)
	}

	if _, err := e.st.Pool.Exec(ctx, `UPDATE problems SET state = 'invalid' WHERE id = $1`, pid); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetProblemState(ctx, pid, poster, store.ProblemStateOpen, ""); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("reopen invalid: %v", err)
	}
}
