package service

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
)

// PosterSilence is how long the poster must have been silent (no state
// change, no revision on the problem) before the community may vote it solved.
const PosterSilence = 14 * 24 * time.Hour

// defaultStateVoteThreshold is used when RankWeights.StateVoteThreshold is unset.
const defaultStateVoteThreshold = 10

// communityVoteReason is the reason on the system state event a community vote writes.
const communityVoteReason = "Community vote"

// StateVoteThreshold is the summed weight at which a community vote flips a problem.
func (s *Service) StateVoteThreshold() float64 {
	if t := s.Cfg.RankWeights.StateVoteThreshold; t > 0 {
		return t
	}
	return defaultStateVoteThreshold
}

// CanVoteState reports whether a signed-in non-poster may add a community
// vote to move a problem in state `from` to `to`:
//   - invalid: only a domain_contributor or domain_expert in the problem's
//     domain, while the problem is open or solved;
//   - solved: anyone, while the problem is open, once the poster has been
//     silent for PosterSilence (measured from now).
func CanVoteState(to, from store.ProblemState, viewerTier string, posterLastActive, now time.Time) bool {
	switch to {
	case store.ProblemStateInvalid:
		return from != store.ProblemStateInvalid && provenTier(viewerTier)
	case store.ProblemStateSolved:
		return from == store.ProblemStateOpen && !now.Before(posterLastActive.Add(PosterSilence))
	}
	return false
}

// VotePercent is a tally as a whole percentage of the threshold (capped at 100).
func VotePercent(sum, threshold float64) int {
	if sum <= 0 || threshold <= 0 {
		return 0
	}
	return int(math.Min(100, math.Floor(sum/threshold*100)))
}

// ChangeProblemState is POST /p/{id}/state. The poster marks solved or
// reopens (SetProblemState); anyone else may only cast a community vote for
// solved or invalid (ToggleStateVote).
func (s *Service) ChangeProblemState(ctx context.Context, problemID, actorID uuid.UUID, to store.ProblemState, reason string) error {
	p, err := s.Store.GetProblemForWrite(ctx, problemID)
	if err != nil {
		return notFound(err)
	}
	if p.AuthorID == actorID {
		return s.SetProblemState(ctx, problemID, actorID, to, reason)
	}
	if to != store.ProblemStateSolved && to != store.ProblemStateInvalid {
		return ErrForbidden
	}
	_, err = s.ToggleStateVote(ctx, problemID, actorID, to)
	return err
}

// StateVoteResult is the outcome of a community state vote toggle.
type StateVoteResult struct {
	Voted   bool // the voter now has a vote for this state
	Percent int  // tally as a percentage of the threshold (0 after a flip)
	Flipped bool // this vote moved the problem to the target state
}

// ToggleStateVote records (or withdraws) a weighted community vote to move a
// problem to solved or invalid. In one transaction: lock the problem, toggle
// the problem_state_votes row (weight = the voter's current vote weight in
// the problem's domain), sum the weights for that state and, at the
// threshold, flip the state, write a system problem_state_events row
// ("Community vote"), clear that state's votes and enqueue RecomputeStanding.
// Withdrawing is always allowed; adding a vote requires CanVoteState.
func (s *Service) ToggleStateVote(ctx context.Context, problemID, voterID uuid.UUID, to store.ProblemState) (StateVoteResult, error) {
	var res StateVoteResult
	if to != store.ProblemStateSolved && to != store.ProblemStateInvalid {
		return res, Invalid("to", "The community can vote a problem solved or invalid.")
	}
	p, err := s.Store.GetProblemForWrite(ctx, problemID)
	if err != nil {
		return res, notFound(err)
	}
	if p.AuthorID == voterID {
		return res, ErrForbidden
	}
	tier, err := s.Store.GetStandingTier(ctx, store.GetStandingTierParams{UserID: voterID, DomainID: p.DomainID})
	if err != nil {
		return res, err
	}
	w, err := s.Weigher.Weight(ctx, voterID, p.DomainID)
	if err != nil {
		return res, err
	}
	threshold := s.StateVoteThreshold()
	err = s.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		lp, err := q.LockProblemForStateVote(ctx, problemID)
		if err != nil {
			return notFound(err)
		}
		if lp.State == store.ProblemStateInvalid {
			return ErrForbidden
		}
		key := store.DeleteStateVoteParams{ProblemID: problemID, UserID: voterID, ToState: to}
		n, err := q.DeleteStateVote(ctx, key)
		if err != nil {
			return err
		}
		now := s.Now()
		if n == 0 {
			if !CanVoteState(to, lp.State, tier, lp.PosterLastActiveAt, now) {
				return ErrForbidden
			}
			if _, err := q.InsertStateVote(ctx, store.InsertStateVoteParams{
				ProblemID: problemID, UserID: voterID, ToState: to, Weight: float32(w), CreatedAt: now,
			}); err != nil {
				return err
			}
			res.Voted = true
		}
		sum, err := q.SumStateVotes(ctx, store.SumStateVotesParams{ProblemID: problemID, ToState: to})
		if err != nil {
			return err
		}
		res.Percent = VotePercent(sum, threshold)
		if !res.Voted || sum < threshold || lp.State == to {
			return nil
		}
		if err := q.WriteProblemState(ctx, store.WriteProblemStateParams{ID: problemID, State: to, Now: now}); err != nil {
			return err
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if err := q.AppendProblemStateEvent(ctx, store.AppendProblemStateEventParams{
			ID: id, ProblemID: problemID, FromState: lp.State, ToState: to,
			ActorID: nil, Reason: communityVoteReason, CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := q.ClearStateVotes(ctx, store.ClearStateVotesParams{ProblemID: problemID, ToState: to}); err != nil {
			return err
		}
		res.Flipped, res.Percent = true, 0
		return s.enqueueStandingForStateChange(ctx, q, tx, problemID, lp.AuthorID, lp.DomainID)
	})
	return res, err
}

// CommunityVote is the problem page's view of the community state vote.
type CommunityVote struct {
	SolvedPercent, InvalidPercent int
	VotedSolved, VotedInvalid     bool
	CanVoteSolved, CanVoteInvalid bool // may add a vote (withdrawing is always allowed)
	SolvedOpensAt                 time.Time
}

// communityVote builds the page view from the problem page row. viewer is nil
// when signed out; the poster gets no community vote.
func (s *Service) communityVote(p store.GetProblemPageRow, signedIn bool) CommunityVote {
	th := s.StateVoteThreshold()
	cv := CommunityVote{
		SolvedPercent:  VotePercent(p.SolvedVoteWeight, th),
		InvalidPercent: VotePercent(p.InvalidVoteWeight, th),
		VotedSolved:    p.ViewerVotedSolved,
		VotedInvalid:   p.ViewerVotedInvalid,
		SolvedOpensAt:  p.PosterLastActiveAt.Add(PosterSilence),
	}
	if signedIn && !p.ViewerIsPoster {
		now := s.Now()
		cv.CanVoteSolved = CanVoteState(store.ProblemStateSolved, p.State, p.ViewerTier, p.PosterLastActiveAt, now)
		cv.CanVoteInvalid = CanVoteState(store.ProblemStateInvalid, p.State, p.ViewerTier, p.PosterLastActiveAt, now)
	}
	return cv
}
