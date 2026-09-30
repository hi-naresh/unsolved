package service

import (
	"context"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// InterestResult is the founder-interest state after a toggle.
type InterestResult struct {
	Interested bool
	Count      int64
}

// ToggleInterest flips the user's founder interest in a problem and returns
// the new count (counted over the problem's rows; the PK index bounds it).
func (s *Service) ToggleInterest(ctx context.Context, problemID, userID uuid.UUID) (InterestResult, error) {
	var res InterestResult
	p, err := s.Store.GetProblemForWrite(ctx, problemID)
	if err != nil {
		return res, notFound(err)
	}
	if p.State == store.ProblemStateInvalid {
		return res, ErrForbidden
	}
	err = s.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
		n, err := q.InsertFounderInterest(ctx, store.InsertFounderInterestParams{
			ProblemID: problemID, UserID: userID, CreatedAt: s.Now(),
		})
		if err != nil {
			return err
		}
		res.Interested = n == 1
		if n == 0 {
			if _, err := q.DeleteFounderInterest(ctx, store.DeleteFounderInterestParams{ProblemID: problemID, UserID: userID}); err != nil {
				return err
			}
		}
		res.Count, err = q.CountFounderInterest(ctx, problemID)
		return err
	})
	return res, err
}
