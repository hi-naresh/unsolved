package jobs

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// TxInserter is the part of the River client a worker uses to enqueue a
// follow-up job inside its own transaction.
type TxInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// PickCurrentRevisionWorker picks a problem's current revision from stored
// scores. Idempotent: running it twice changes nothing the second time.
type PickCurrentRevisionWorker struct {
	river.WorkerDefaults[PickCurrentRevisionArgs]
	d Deps
}

func (w *PickCurrentRevisionWorker) Work(ctx context.Context, job *river.Job[PickCurrentRevisionArgs]) error {
	return PickCurrentRevision(ctx, w.d, job.Args.ProblemID)
}

// PickCurrentSolutionRevisionWorker picks a solution's current revision, then
// recomputes the problem's soft_solved flag.
type PickCurrentSolutionRevisionWorker struct {
	river.WorkerDefaults[PickCurrentSolutionRevisionArgs]
	d Deps
}

func (w *PickCurrentSolutionRevisionWorker) Work(ctx context.Context, job *river.Job[PickCurrentSolutionRevisionArgs]) error {
	return PickCurrentSolutionRevision(ctx, w.d, clientFrom(ctx), job.Args.SolutionID)
}

// clientFrom returns the River client running this job (nil outside a worker).
func clientFrom(ctx context.Context) TxInserter {
	c, err := river.ClientFromContextSafely[pgx.Tx](ctx)
	if err != nil {
		return nil
	}
	return c
}

// nowFn is d.Now, or the UTC wall clock when a caller left it unset.
func nowFn(d Deps) func() time.Time {
	if d.Now != nil {
		return d.Now
	}
	return func() time.Time { return time.Now().UTC() }
}

// PickCurrentRevision reads the problem's revisions ordered by stored score,
// applies ranking.PickLeader (tie → incumbent; a challenger needs
// RANK_MIN_VOTES_TO_TAKE_OVER votes) and sets current_revision_id and
// problems.score in one statement.
func PickCurrentRevision(ctx context.Context, d Deps, problemID uuid.UUID) error {
	return d.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
		inc, err := q.LockProblemForPick(ctx, problemID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		rows, err := q.ListProblemRevisionCandidates(ctx, problemID)
		if err != nil {
			return err
		}
		cands := make([]ranking.Candidate, len(rows))
		for i, r := range rows {
			cands[i] = ranking.Candidate{ID: r.ID, Score: r.Score, VoteCount: r.VoteCount}
		}
		incumbent := deref(inc)
		leader := ranking.PickLeader(cands, incumbent, d.Cfg.RankMinVotesToTakeOver)
		if leader == uuid.Nil {
			return nil
		}
		if _, err := q.SetProblemCurrentRevision(ctx, store.SetProblemCurrentRevisionParams{
			ProblemID: problemID, RevisionID: leader, Now: nowFn(d)(),
		}); err != nil {
			return err
		}
		if leader != incumbent && d.Log != nil {
			d.Log.InfoContext(ctx, "current revision changed", "problem_id", problemID, "revision_id", leader)
		}
		return nil
	})
}

// PickCurrentSolutionRevision is PickCurrentRevision for a solution (sets
// solutions.current_revision_id and solutions.score), followed by the
// problem's soft_solved = top solution's current revision vote_count ≥
// SOFT_SOLVED_THRESHOLD. When soft_solved first flips to true it enqueues
// SolvedPrompt through ins in the same transaction (ins may be nil only in
// tests that don't care).
func PickCurrentSolutionRevision(ctx context.Context, d Deps, ins TxInserter, solutionID uuid.UUID) error {
	return d.Store.InTx(ctx, func(q *store.Queries, tx pgx.Tx) error {
		sol, err := q.LockSolutionForPick(ctx, solutionID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		rows, err := q.ListSolutionRevisionCandidates(ctx, solutionID)
		if err != nil {
			return err
		}
		cands := make([]ranking.Candidate, len(rows))
		for i, r := range rows {
			cands[i] = ranking.Candidate{ID: r.ID, Score: r.Score, VoteCount: r.VoteCount}
		}
		leader := ranking.PickLeader(cands, deref(sol.CurrentRevisionID), d.Cfg.RankMinVotesToTakeOver)
		if leader != uuid.Nil {
			if _, err := q.SetSolutionCurrentRevision(ctx, store.SetSolutionCurrentRevisionParams{
				SolutionID: solutionID, RevisionID: leader,
			}); err != nil {
				return err
			}
		}
		return updateSoftSolved(ctx, d, q, tx, ins, sol.ProblemID)
	})
}

func updateSoftSolved(ctx context.Context, d Deps, q *store.Queries, tx pgx.Tx, ins TxInserter, problemID uuid.UUID) error {
	was, err := q.LockProblemSoftSolved(ctx, problemID)
	if err != nil {
		return err
	}
	top, err := q.GetTopSolution(ctx, problemID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	now := err == nil && top.VoteCount >= d.Cfg.SoftSolvedThreshold
	if now == was {
		return nil
	}
	if err := q.SetProblemSoftSolved(ctx, store.SetProblemSoftSolvedParams{ID: problemID, SoftSolved: now}); err != nil {
		return err
	}
	if now && ins != nil {
		if _, err := ins.InsertTx(ctx, tx, SolvedPromptArgs{ProblemID: problemID, SolutionID: top.ID}, nil); err != nil {
			return err
		}
	}
	return nil
}

func deref(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}
