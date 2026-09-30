package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// NewSolution is the post-a-solution form.
type NewSolution struct {
	Kind    store.SolutionKind
	Body    string
	Display store.DisplayMode
}

// CreateSolution inserts the solution, its first revision and points
// current_revision_id at it, in one transaction.
func (s *Service) CreateSolution(ctx context.Context, problemID, authorID uuid.UUID, in NewSolution) (uuid.UUID, error) {
	var errs []error
	if !in.Kind.Valid() {
		errs = append(errs, Invalid("kind", "Choose what kind of solution this is."))
	}
	in.Body = contentText(in.Body)
	contentLen(&errs, "body", "The solution", in.Body, solutionBodyMin, solutionBodyMax)
	display, err := contentDisplay(in.Display)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return uuid.Nil, errors.Join(errs...)
	}
	p, err := s.Store.GetProblemForWrite(ctx, problemID)
	if err != nil {
		return uuid.Nil, notFound(err)
	}
	if p.State == store.ProblemStateInvalid {
		return uuid.Nil, ErrForbidden
	}
	sid, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	rid, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	now := s.Now()
	err = s.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
		if err := q.InsertSolution(ctx, store.InsertSolutionParams{
			ID: sid, ProblemID: problemID, AuthorID: authorID, AuthorDisplay: display,
			Kind: in.Kind, CurrentRevisionID: &rid, CreatedAt: now,
		}); err != nil {
			return err
		}
		return q.InsertSolutionRevision(ctx, store.InsertSolutionRevisionParams{
			ID: rid, SolutionID: sid, AuthorID: authorID, AuthorDisplay: display, Body: in.Body, CreatedAt: now,
		})
	})
	if err != nil {
		return uuid.Nil, err
	}
	return sid, nil
}

// NewSolutionRevision is the inline "suggest a revision" form.
type NewSolutionRevision struct {
	ParentRevisionID uuid.UUID
	Body             string
	WhyNote          string
	Display          store.DisplayMode
}

// CreateSolutionRevision adds a child revision to a solution. why_note is
// required. It returns the problem id (for the redirect).
func (s *Service) CreateSolutionRevision(ctx context.Context, solutionID, authorID uuid.UUID, in NewSolutionRevision) (uuid.UUID, error) {
	var errs []error
	in.Body = contentText(in.Body)
	contentLen(&errs, "body", "The solution", in.Body, solutionBodyMin, solutionBodyMax)
	in.WhyNote = contentText(in.WhyNote)
	contentLen(&errs, "why_note", "The note on why you changed it", in.WhyNote, 1, whyNoteMax)
	display, err := contentDisplay(in.Display)
	if err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return uuid.Nil, errors.Join(errs...)
	}
	sol, err := s.Store.GetSolutionForWrite(ctx, solutionID)
	if err != nil {
		return uuid.Nil, notFound(err)
	}
	if sol.State == store.ProblemStateInvalid {
		return uuid.Nil, ErrForbidden
	}
	parent, err := s.Store.GetSolutionRevisionOwner(ctx, in.ParentRevisionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, err
	}
	if err != nil || parent.SolutionID != solutionID {
		return uuid.Nil, Invalid("parent_revision_id", "That revision doesn't belong to this solution.")
	}
	rid, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, err
	}
	why := in.WhyNote
	if err := s.Store.InsertSolutionRevision(ctx, store.InsertSolutionRevisionParams{
		ID: rid, SolutionID: solutionID, ParentRevisionID: &in.ParentRevisionID,
		AuthorID: authorID, AuthorDisplay: display, Body: in.Body, WhyNote: &why, CreatedAt: s.Now(),
	}); err != nil {
		return uuid.Nil, err
	}
	return sol.ProblemID, nil
}

// RecordTrial records (or replaces) the user's "tried it" outcome for a
// solution. It returns the problem id (for the redirect).
func (s *Service) RecordTrial(ctx context.Context, solutionID, userID uuid.UUID, outcome store.TriedOutcome, note string) (uuid.UUID, error) {
	var errs []error
	if !outcome.Valid() {
		errs = append(errs, Invalid("outcome", "Choose worked, partly or failed."))
	}
	note = contentText(note)
	contentLen(&errs, "note", "The note", note, 0, trialNoteMax)
	if len(errs) > 0 {
		return uuid.Nil, errors.Join(errs...)
	}
	sol, err := s.Store.GetSolutionForWrite(ctx, solutionID)
	if err != nil {
		return uuid.Nil, notFound(err)
	}
	if sol.State == store.ProblemStateInvalid {
		return uuid.Nil, ErrForbidden
	}
	var np *string
	if note != "" {
		np = &note
	}
	err = s.Store.UpsertSolutionTrial(ctx, store.UpsertSolutionTrialParams{
		SolutionID: solutionID, UserID: userID, Outcome: outcome, Note: np, CreatedAt: s.Now(),
	})
	return sol.ProblemID, err
}
