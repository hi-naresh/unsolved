package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// rescoreBatch is the number of revisions rebuilt per transaction.
const rescoreBatch = 500

// RescoreAllWorker rebuilds every stored score from the vote tables.
type RescoreAllWorker struct {
	river.WorkerDefaults[RescoreAllArgs]
	d Deps
}

func (w *RescoreAllWorker) Work(ctx context.Context, job *river.Job[RescoreAllArgs]) error {
	return RescoreAll(ctx, w.d, clientFrom(ctx), job.Args)
}

// RescoreAll rebuilds score and vote_count of every problem revision and
// solution revision from the vote rows, 500 revisions per transaction (keyset
// by id): score = Σ weight · 2^((created_at − t0)/h), via the same
// Scorer.Contribution the vote path uses. Each batch locks its revisions, so a
// concurrent vote waits and then adds its delta on top of the rebuilt score.
// Afterwards every affected problem and solution has its current revision
// (and soft_solved) recomputed. With args.ProblemIDs set, only revisions of
// those problems and of their solutions are rebuilt. Idempotent.
func RescoreAll(ctx context.Context, d Deps, ins TxInserter, args RescoreAllArgs) error {
	only := len(args.ProblemIDs) > 0
	problems := map[uuid.UUID]struct{}{}
	solutions := map[uuid.UUID]struct{}{}

	after := uuid.Nil
	for {
		var n int
		err := d.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
			rows, err := q.LockProblemRevisionsForRescore(ctx, store.LockProblemRevisionsForRescoreParams{
				AfterID: after, OnlyListed: only, ProblemIds: args.ProblemIDs, Lim: rescoreBatch,
			})
			if err != nil || len(rows) == 0 {
				return err
			}
			n = len(rows)
			ids := make([]uuid.UUID, len(rows))
			for i, r := range rows {
				ids[i] = r.ID
				problems[r.ProblemID] = struct{}{}
			}
			after = ids[len(ids)-1]
			votes, err := q.ListProblemRevisionVotesFor(ctx, ids)
			if err != nil {
				return err
			}
			scores, counts := make(map[uuid.UUID]float64, n), make(map[uuid.UUID]int32, n)
			for _, v := range votes {
				scores[v.RevisionID] += d.Scorer.Contribution(float64(v.Weight), v.CreatedAt)
				counts[v.RevisionID]++
			}
			sc, cn := rescoreColumns(ids, scores, counts)
			return q.SetProblemRevisionScores(ctx, store.SetProblemRevisionScoresParams{Ids: ids, Scores: sc, Counts: cn})
		})
		if err != nil {
			return err
		}
		if n < rescoreBatch {
			break
		}
	}

	after = uuid.Nil
	for {
		var n int
		err := d.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
			rows, err := q.LockSolutionRevisionsForRescore(ctx, store.LockSolutionRevisionsForRescoreParams{
				AfterID: after, OnlyListed: only, ProblemIds: args.ProblemIDs, Lim: rescoreBatch,
			})
			if err != nil || len(rows) == 0 {
				return err
			}
			n = len(rows)
			ids := make([]uuid.UUID, len(rows))
			for i, r := range rows {
				ids[i] = r.ID
				solutions[r.SolutionID] = struct{}{}
			}
			after = ids[len(ids)-1]
			votes, err := q.ListSolutionRevisionVotesFor(ctx, ids)
			if err != nil {
				return err
			}
			scores, counts := make(map[uuid.UUID]float64, n), make(map[uuid.UUID]int32, n)
			for _, v := range votes {
				scores[v.RevisionID] += d.Scorer.Contribution(float64(v.Weight), v.CreatedAt)
				counts[v.RevisionID]++
			}
			sc, cn := rescoreColumns(ids, scores, counts)
			return q.SetSolutionRevisionScores(ctx, store.SetSolutionRevisionScoresParams{Ids: ids, Scores: sc, Counts: cn})
		})
		if err != nil {
			return err
		}
		if n < rescoreBatch {
			break
		}
	}

	for id := range problems {
		if err := PickCurrentRevision(ctx, d, id); err != nil {
			return err
		}
	}
	for id := range solutions {
		if err := PickCurrentSolutionRevision(ctx, d, ins, id); err != nil {
			return err
		}
	}
	if d.Log != nil {
		d.Log.InfoContext(ctx, "rescore finished", "problems", len(problems), "solutions", len(solutions), "restricted", only)
	}
	return nil
}

func rescoreColumns(ids []uuid.UUID, scores map[uuid.UUID]float64, counts map[uuid.UUID]int32) ([]float64, []int32) {
	sc := make([]float64, len(ids))
	cn := make([]int32, len(ids))
	for i, id := range ids {
		sc[i] = scores[id]
		cn[i] = counts[id]
	}
	return sc, cn
}
