package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// The one-tap outcome link from the SolvedPrompt email (phase 3). The signed
// token (made by the SolvedPrompt job) authenticates the poster for exactly
// one action: recording their outcome for one solution; no sign-in needed.

var (
	// ErrLinkInvalid: the token is malformed, tampered with or signed with a
	// rotated key. The handler answers 400.
	ErrLinkInvalid = errors.New("outcome link invalid")
	// ErrLinkExpired: the token is genuine but older than 14 days. 410.
	ErrLinkExpired = errors.New("outcome link expired")
)

// EmailOutcomeNote is stored as the note of a trial recorded from the email.
const EmailOutcomeNote = "via email"

// EmailOutcomeReason is the problem_state_events reason when the poster
// confirms "worked" from the email.
const EmailOutcomeReason = "Poster confirmed via email"

// MakeOutcomeToken signs an outcome token with this service's key (tests and
// tooling; the SolvedPrompt job signs its own with the same key).
func (s *Service) MakeOutcomeToken(c jobs.OutcomeClaims) string {
	return jobs.MakeOutcomeToken(s.Cfg.SigningKey(jobs.OutcomeTokenPurpose), c)
}

// ParseOutcomeToken verifies an emailed token, mapping failures to
// ErrLinkInvalid / ErrLinkExpired.
func (s *Service) ParseOutcomeToken(token string) (jobs.OutcomeClaims, error) {
	c, err := jobs.ParseOutcomeToken(s.Cfg.SigningKey(jobs.OutcomeTokenPurpose), token, s.Now())
	switch {
	case errors.Is(err, jobs.ErrOutcomeTokenExpired):
		return c, ErrLinkExpired
	case err != nil:
		return c, ErrLinkInvalid
	}
	return c, nil
}

// OutcomePrompt is what the GET confirm page shows.
type OutcomePrompt struct {
	ProblemID     uuid.UUID
	SolutionID    uuid.UUID
	Title         string
	Kind          store.SolutionKind
	Body          string
	State         store.ProblemState
	PosterOutcome string // the poster's existing outcome for this solution, or ""
}

// OutcomePrompt verifies token and loads the confirm page. It changes
// nothing: email scanners prefetch links. A deleted or suspended poster is
// ErrForbidden.
func (s *Service) OutcomePrompt(ctx context.Context, token string) (OutcomePrompt, error) {
	c, err := s.ParseOutcomeToken(token)
	if err != nil {
		return OutcomePrompt{}, err
	}
	row, err := s.Store.GetSolvedPromptContext(ctx, store.GetSolvedPromptContextParams{
		ProblemID: c.ProblemID, SolutionID: c.SolutionID,
	})
	if err != nil {
		return OutcomePrompt{}, notFound(err)
	}
	if row.AuthorID != c.PosterID || row.AuthorDeleted || row.AuthorSuspended {
		return OutcomePrompt{}, ErrForbidden
	}
	return OutcomePrompt{
		ProblemID: row.ProblemID, SolutionID: row.SolutionID, Title: row.Title,
		Kind: row.Kind, Body: row.Body, State: row.State, PosterOutcome: row.PosterOutcome,
	}, nil
}

// RecordEmailOutcome records the poster's outcome from the emailed link, in
// one transaction: upsert their solution_trials row (note "via email"), and
// for "worked" also mark an open problem solved with a problem_state_events
// row (actor = poster). An already-solved problem is left as it is, so
// repeating the POST is harmless. It returns the claims (for the redirect).
func (s *Service) RecordEmailOutcome(ctx context.Context, token string, outcome store.TriedOutcome) (jobs.OutcomeClaims, error) {
	c, err := s.ParseOutcomeToken(token)
	if err != nil {
		return c, err
	}
	if !outcome.Valid() {
		return c, Invalid("outcome", "Choose worked, partly or failed.")
	}
	err = s.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
		p, err := q.LockOutcomeTarget(ctx, store.LockOutcomeTargetParams{ProblemID: c.ProblemID, SolutionID: c.SolutionID})
		if err != nil {
			return notFound(err)
		}
		if p.AuthorID != c.PosterID || p.AuthorDeleted || p.AuthorSuspended || p.State == store.ProblemStateInvalid {
			return ErrForbidden
		}
		now := s.Now()
		note := EmailOutcomeNote
		if err := q.UpsertSolutionTrial(ctx, store.UpsertSolutionTrialParams{
			SolutionID: c.SolutionID, UserID: c.PosterID, Outcome: outcome, Note: &note, CreatedAt: now,
		}); err != nil {
			return err
		}
		if outcome != store.TriedOutcomeWorked || p.State != store.ProblemStateOpen {
			return nil
		}
		if err := q.WriteProblemState(ctx, store.WriteProblemStateParams{ID: c.ProblemID, State: store.ProblemStateSolved, Now: now}); err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		actor := c.PosterID
		return q.AppendProblemStateEvent(ctx, store.AppendProblemStateEventParams{
			ID: id, ProblemID: c.ProblemID, FromState: p.State, ToState: store.ProblemStateSolved,
			ActorID: &actor, Reason: EmailOutcomeReason, CreatedAt: now,
		})
	})
	return c, err
}
