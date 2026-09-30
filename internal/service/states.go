package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// SetProblemState is the poster's control: mark an open problem solved, or
// reopen a solved one. Every change writes a problem_state_events row in the
// same transaction. Invalid is admin-only (and phase-4 community votes), so
// it is refused here, as is any change to an invalid problem.
func (s *Service) SetProblemState(ctx context.Context, problemID, actorID uuid.UUID, to store.ProblemState, reason string) error {
	var errs []error
	if to != store.ProblemStateOpen && to != store.ProblemStateSolved {
		errs = append(errs, Invalid("state", "You can mark a problem solved or reopen it."))
	}
	reason = contentText(reason)
	contentLen(&errs, "reason", "The reason", reason, 0, stateReasonMax)
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return s.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
		p, err := q.LockProblemState(ctx, problemID)
		if err != nil {
			return notFound(err)
		}
		if p.AuthorID != actorID || p.State == store.ProblemStateInvalid {
			return ErrForbidden
		}
		if p.State == to {
			return ErrConflict
		}
		now := s.Now()
		if err := q.WriteProblemState(ctx, store.WriteProblemStateParams{ID: problemID, State: to, Now: now}); err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		return q.AppendProblemStateEvent(ctx, store.AppendProblemStateEventParams{
			ID: id, ProblemID: problemID, FromState: p.State, ToState: to,
			ActorID: &actorID, Reason: reason, CreatedAt: now,
		})
	})
}
