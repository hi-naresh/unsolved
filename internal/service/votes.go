package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// VoteResult is the state after a toggle: whether the viewer now has a vote
// on the revision, the revision's vote count, and the problem it belongs to
// (for the no-JS redirect).
type VoteResult struct {
	Voted     bool
	Count     int32
	ProblemID uuid.UUID
}

// ToggleProblemVote is the vote flow, in one transaction:
//  1. insert the vote with the voter's current weight, or, if it already
//     exists, delete it (using the stored weight and created_at);
//  2. a single-row UPDATE of the revision's score and vote_count;
//  3. enqueue PickCurrentRevision (unique per problem over 5 s).
func (s *Service) ToggleProblemVote(ctx context.Context, revisionID, voterID uuid.UUID) (VoteResult, error) {
	t, err := s.Store.GetProblemRevisionVoteTarget(ctx, revisionID)
	if err != nil {
		return VoteResult{}, notFound(err)
	}
	if t.AuthorID == voterID || t.State == store.ProblemStateInvalid {
		return VoteResult{}, ErrForbidden
	}
	w, err := s.Weigher.Weight(ctx, voterID, t.DomainID)
	if err != nil {
		return VoteResult{}, err
	}
	res := VoteResult{ProblemID: t.ProblemID}
	now := s.Now()
	err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		voted, delta, dc, err := s.toggleVoteRow(
			func() (float32, time.Time, error) {
				r, err := q.InsertProblemRevisionVote(ctx, store.InsertProblemRevisionVoteParams{
					RevisionID: revisionID, UserID: voterID, Weight: float32(w), CreatedAt: now,
				})
				return r.Weight, r.CreatedAt, err
			},
			func() (float32, time.Time, error) {
				r, err := q.DeleteProblemRevisionVote(ctx, store.DeleteProblemRevisionVoteParams{RevisionID: revisionID, UserID: voterID})
				return r.Weight, r.CreatedAt, err
			})
		if err != nil {
			return err
		}
		res.Voted = voted
		if res.Count, err = q.AddProblemRevisionScore(ctx, store.AddProblemRevisionScoreParams{
			ID: revisionID, Delta: delta, CountDelta: dc,
		}); err != nil {
			return err
		}
		return s.enqueueContentJob(ctx, tx, jobs.PickCurrentRevisionArgs{ProblemID: t.ProblemID})
	})
	return res, err
}

// ToggleSolutionVote is ToggleProblemVote for solution revisions; it
// enqueues PickCurrentSolutionRevision (unique per solution over 5 s).
func (s *Service) ToggleSolutionVote(ctx context.Context, revisionID, voterID uuid.UUID) (VoteResult, error) {
	t, err := s.Store.GetSolutionRevisionVoteTarget(ctx, revisionID)
	if err != nil {
		return VoteResult{}, notFound(err)
	}
	if t.AuthorID == voterID || t.State == store.ProblemStateInvalid {
		return VoteResult{}, ErrForbidden
	}
	w, err := s.Weigher.Weight(ctx, voterID, t.DomainID)
	if err != nil {
		return VoteResult{}, err
	}
	res := VoteResult{ProblemID: t.ProblemID}
	now := s.Now()
	err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		voted, delta, dc, err := s.toggleVoteRow(
			func() (float32, time.Time, error) {
				r, err := q.InsertSolutionRevisionVote(ctx, store.InsertSolutionRevisionVoteParams{
					RevisionID: revisionID, UserID: voterID, Weight: float32(w), CreatedAt: now,
				})
				return r.Weight, r.CreatedAt, err
			},
			func() (float32, time.Time, error) {
				r, err := q.DeleteSolutionRevisionVote(ctx, store.DeleteSolutionRevisionVoteParams{RevisionID: revisionID, UserID: voterID})
				return r.Weight, r.CreatedAt, err
			})
		if err != nil {
			return err
		}
		res.Voted = voted
		if res.Count, err = q.AddSolutionRevisionScore(ctx, store.AddSolutionRevisionScoreParams{
			ID: revisionID, Delta: delta, CountDelta: dc,
		}); err != nil {
			return err
		}
		return s.enqueueContentJob(ctx, tx, jobs.PickCurrentSolutionRevisionArgs{SolutionID: t.SolutionID})
	})
	return res, err
}

// toggleVoteRow runs INSERT … ON CONFLICT DO NOTHING RETURNING; if nothing
// was inserted the vote exists, so DELETE … RETURNING it. If a concurrent
// request removed it in between, try once more. The score delta is computed
// from the row's stored weight and created_at, exactly as RescoreAll does.
func (s *Service) toggleVoteRow(ins, del func() (float32, time.Time, error)) (voted bool, delta float64, countDelta int32, err error) {
	for range 2 {
		w, t, err := ins()
		if err == nil {
			return true, s.Scorer.Contribution(float64(w), t), 1, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, 0, 0, err
		}
		w, t, err = del()
		if err == nil {
			return false, -s.Scorer.Contribution(float64(w), t), -1, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return false, 0, 0, err
		}
	}
	return false, 0, 0, ErrConflict
}
