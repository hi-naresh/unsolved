package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/service"
	"github.com/hi-naresh/unsolved/internal/store"
)

// outcomeFixture: a problem by poster with one solution by helper, and a
// fresh token for it.
type outcomeFixture struct {
	poster, helper, problem, solution uuid.UUID
	token                             string
}

func newOutcomeFixture(t *testing.T, e *contentEnv) outcomeFixture {
	t.Helper()
	ctx := context.Background()
	f := outcomeFixture{poster: e.user(t), helper: e.user(t)}
	f.problem, _ = e.problem(t, f.poster, "Chasing delivery notes by email")
	sid, err := e.svc.CreateSolution(ctx, f.problem, f.helper, sampleSolution())
	if err != nil {
		t.Fatal(err)
	}
	f.solution = sid
	f.token = e.svc.MakeOutcomeToken(jobs.OutcomeClaims{
		ProblemID: f.problem, SolutionID: f.solution, PosterID: f.poster, Expires: e.svc.Now().Add(jobs.OutcomeTokenTTL),
	})
	return f
}

func (e *contentEnv) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := e.st.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

func (e *contentEnv) outcomeState(t *testing.T, f outcomeFixture) (state, trial, note string, events int) {
	t.Helper()
	ctx := context.Background()
	if err := e.st.Pool.QueryRow(ctx, `
		SELECT p.state::text,
		       COALESCE((SELECT outcome::text FROM solution_trials WHERE solution_id = $2 AND user_id = p.author_id), ''),
		       COALESCE((SELECT note FROM solution_trials WHERE solution_id = $2 AND user_id = p.author_id), ''),
		       (SELECT count(*) FROM problem_state_events WHERE problem_id = p.id)::int
		FROM problems p WHERE p.id = $1`, f.problem, f.solution).Scan(&state, &trial, &note, &events); err != nil {
		t.Fatal(err)
	}
	return
}

func TestParseOutcomeTokenMapsErrors(t *testing.T) {
	e := newContentEnv(t)
	c := jobs.OutcomeClaims{ProblemID: uuid.New(), SolutionID: uuid.New(), PosterID: uuid.New(), Expires: e.svc.Now().Add(time.Hour)}
	tok := e.svc.MakeOutcomeToken(c)
	cases := []struct {
		name  string
		token string
		now   time.Time
		want  error
	}{
		{"valid", tok, e.svc.Now(), nil},
		{"expired", tok, e.svc.Now().Add(2 * time.Hour), service.ErrLinkExpired},
		{"tampered", tok[:len(tok)-2] + "AA", e.svc.Now(), service.ErrLinkInvalid},
		{"garbage", "hello", e.svc.Now(), service.ErrLinkInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := *e.svc
			now := tc.now
			svc.Now = func() time.Time { return now }
			got, err := svc.ParseOutcomeToken(tc.token)
			if !errors.Is(err, tc.want) && err != tc.want {
				t.Fatalf("err %v, want %v", err, tc.want)
			}
			if tc.want == nil && (got.ProblemID != c.ProblemID || got.PosterID != c.PosterID) {
				t.Fatalf("claims %+v", got)
			}
		})
	}
	// Another signing key (e.g. a rotated secret) invalidates the link.
	svc := *e.svc
	svc.Cfg.PostmarkToken = "rotated"
	if _, err := svc.ParseOutcomeToken(tok); !errors.Is(err, service.ErrLinkInvalid) {
		t.Fatalf("rotated key: %v", err)
	}
}

func TestOutcomePromptChangesNothing(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()
	f := newOutcomeFixture(t, e)
	p, err := e.svc.OutcomePrompt(ctx, f.token)
	if err != nil {
		t.Fatal(err)
	}
	if p.ProblemID != f.problem || p.SolutionID != f.solution || p.Title != "Chasing delivery notes by email" ||
		p.Kind != store.SolutionKindOffTheShelf || p.Body != sampleSolution().Body || p.State != store.ProblemStateOpen || p.PosterOutcome != "" {
		t.Fatalf("prompt %+v", p)
	}
	if state, trial, _, events := e.outcomeState(t, f); state != "open" || trial != "" || events != 0 {
		t.Fatalf("GET changed state: %s %q %d", state, trial, events)
	}

	// A solution that isn't this problem's → not found.
	other := newOutcomeFixture(t, e)
	mixed := e.svc.MakeOutcomeToken(jobs.OutcomeClaims{ProblemID: f.problem, SolutionID: other.solution, PosterID: f.poster, Expires: e.svc.Now().Add(time.Hour)})
	if _, err := e.svc.OutcomePrompt(ctx, mixed); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("mixed ids: %v", err)
	}
	// Signed for someone who isn't the poster → forbidden.
	notPoster := e.svc.MakeOutcomeToken(jobs.OutcomeClaims{ProblemID: f.problem, SolutionID: f.solution, PosterID: f.helper, Expires: e.svc.Now().Add(time.Hour)})
	if _, err := e.svc.OutcomePrompt(ctx, notPoster); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("not poster: %v", err)
	}
	e.exec(t, `UPDATE users SET suspended_at = now() WHERE id = $1`, f.poster)
	if _, err := e.svc.OutcomePrompt(ctx, f.token); !errors.Is(err, service.ErrForbidden) {
		t.Fatalf("suspended poster: %v", err)
	}
}

func TestRecordEmailOutcome(t *testing.T) {
	e := newContentEnv(t)
	ctx := context.Background()

	cases := []struct {
		name       string
		setup      func(t *testing.T, f *outcomeFixture)
		outcomes   []store.TriedOutcome // posted in order; the last one's result is checked
		wantErr    error
		wantState  string
		wantTrial  string
		wantEvents int
	}{
		{name: "partly records the trial only", outcomes: []store.TriedOutcome{"partly"},
			wantState: "open", wantTrial: "partly"},
		{name: "failed records the trial only", outcomes: []store.TriedOutcome{"failed"},
			wantState: "open", wantTrial: "failed"},
		{name: "worked marks solved with an event", outcomes: []store.TriedOutcome{"worked"},
			wantState: "solved", wantTrial: "worked", wantEvents: 1},
		{name: "repeating worked adds no second event", outcomes: []store.TriedOutcome{"worked", "worked"},
			wantState: "solved", wantTrial: "worked", wantEvents: 1},
		{name: "changing mind replaces the trial", outcomes: []store.TriedOutcome{"failed", "partly"},
			wantState: "open", wantTrial: "partly"},
		{name: "bad outcome", outcomes: []store.TriedOutcome{"sort of"},
			wantErr: service.ErrValidation{}, wantState: "open"},
		{name: "expired link", outcomes: []store.TriedOutcome{"worked"},
			setup: func(t *testing.T, f *outcomeFixture) {
				f.token = e.svc.MakeOutcomeToken(jobs.OutcomeClaims{ProblemID: f.problem, SolutionID: f.solution, PosterID: f.poster, Expires: e.svc.Now().Add(-time.Minute)})
			},
			wantErr: service.ErrLinkExpired, wantState: "open"},
		{name: "suspended poster", outcomes: []store.TriedOutcome{"worked"},
			setup: func(t *testing.T, f *outcomeFixture) {
				e.exec(t, `UPDATE users SET suspended_at = now() WHERE id = $1`, f.poster)
			},
			wantErr: service.ErrForbidden, wantState: "open"},
		{name: "deleted poster", outcomes: []store.TriedOutcome{"worked"},
			setup: func(t *testing.T, f *outcomeFixture) {
				e.exec(t, `UPDATE users SET deleted_at = now() WHERE id = $1`, f.poster)
			},
			wantErr: service.ErrForbidden, wantState: "open"},
		{name: "invalid problem", outcomes: []store.TriedOutcome{"worked"},
			setup: func(t *testing.T, f *outcomeFixture) {
				e.exec(t, `UPDATE problems SET state = 'invalid' WHERE id = $1`, f.problem)
			},
			wantErr: service.ErrForbidden, wantState: "invalid"},
		{name: "already solved: trial only", outcomes: []store.TriedOutcome{"worked"},
			setup: func(t *testing.T, f *outcomeFixture) {
				if err := e.svc.SetProblemState(ctx, f.problem, f.poster, store.ProblemStateSolved, ""); err != nil {
					t.Fatal(err)
				}
			},
			wantState: "solved", wantTrial: "worked", wantEvents: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newOutcomeFixture(t, e)
			if tc.setup != nil {
				tc.setup(t, &f)
			}
			var err error
			for _, o := range tc.outcomes {
				var c jobs.OutcomeClaims
				c, err = e.svc.RecordEmailOutcome(ctx, f.token, o)
				if err == nil && (c.ProblemID != f.problem || c.SolutionID != f.solution) {
					t.Fatalf("claims %+v", c)
				}
			}
			var ve service.ErrValidation
			switch {
			case tc.wantErr == nil:
				if err != nil {
					t.Fatal(err)
				}
			case errors.As(tc.wantErr, &ve):
				if !errors.As(err, &ve) {
					t.Fatalf("want validation error, got %v", err)
				}
			case !errors.Is(err, tc.wantErr):
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
			state, trial, note, events := e.outcomeState(t, f)
			if state != tc.wantState || trial != tc.wantTrial || events != tc.wantEvents {
				t.Fatalf("state %s trial %q events %d; want %s %q %d", state, trial, events, tc.wantState, tc.wantTrial, tc.wantEvents)
			}
			if trial != "" && note != service.EmailOutcomeNote {
				t.Fatalf("note %q", note)
			}
		})
	}

	// The event records the poster as actor with the email reason.
	f := newOutcomeFixture(t, e)
	if _, err := e.svc.RecordEmailOutcome(ctx, f.token, store.TriedOutcomeWorked); err != nil {
		t.Fatal(err)
	}
	var actor uuid.UUID
	var from, to, reason string
	if err := e.st.Pool.QueryRow(ctx, `SELECT actor_id, from_state::text, to_state::text, reason FROM problem_state_events WHERE problem_id = $1`, f.problem).
		Scan(&actor, &from, &to, &reason); err != nil {
		t.Fatal(err)
	}
	if actor != f.poster || from != "open" || to != "solved" || reason != service.EmailOutcomeReason {
		t.Fatalf("event: actor %v %s→%s %q", actor, from, to, reason)
	}
}
