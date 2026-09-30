package jobs

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// RecomputeStandingWorker updates user_domain_standing for one user (all of
// their domains, or just DomainID) or, when UserID is nil, for every user
// with activity (the nightly full pass). Idempotent.
type RecomputeStandingWorker struct {
	river.WorkerDefaults[RecomputeStandingArgs]
	d Deps
}

func (w *RecomputeStandingWorker) Work(ctx context.Context, job *river.Job[RecomputeStandingArgs]) error {
	return RecomputeStanding(ctx, w.d, job.Args.UserID, job.Args.DomainID)
}

// Standing points. Points come only from verifiable signals on the site,
// never from declared history, and are never displayed.
const (
	PointsSolvedProblem   = 10 // a problem they posted is marked solved
	PointsCurrentRevision = 5  // a later (non-first) revision of theirs is current
	PointsCurrentFirst    = 2  // their own first revision is current and someone voted for it
	PointsTopSolution     = 5  // their solution tops a soft-solved or solved problem
	VotesPerPoint         = 3  // +1 per 3 net votes received on their revisions
	PointsPerVouch        = 3  // per vouch from a contributor/expert in the domain

	DefaultContributorPoints = 25
	DefaultExpertPoints      = 100
)

// standingBatch is the nightly pass's batch size (users per batch).
const standingBatch = 500

// StandingSignals are the counted signals for one (user, domain).
type StandingSignals struct {
	Solved       float64
	CurrentFirst float64
	CurrentLater float64
	TopSolutions float64
	Votes        float64
	Vouches      float64
}

// StandingPoints turns signals into points.
func StandingPoints(s StandingSignals) float64 {
	votes := math.Floor(math.Max(s.Votes, 0) / VotesPerPoint)
	return PointsSolvedProblem*s.Solved + PointsCurrentRevision*s.CurrentLater + PointsCurrentFirst*s.CurrentFirst +
		PointsTopSolution*s.TopSolutions + votes + PointsPerVouch*s.Vouches
}

// StandingTier maps points to a tier using RankWeights.TierPoints
// (defaults 25 contributor / 100 expert). Below contributor, a user with a
// declared history is declared_background (which weighs no more than member).
func StandingTier(rw config.RankWeights, points float64, declared bool) store.StandingTier {
	contrib, expert := tierPoints(rw)
	switch {
	case points >= expert:
		return store.StandingTierDomainExpert
	case points >= contrib:
		return store.StandingTierDomainContributor
	case declared:
		return store.StandingTierDeclaredBackground
	default:
		return store.StandingTierMember
	}
}

func tierPoints(rw config.RankWeights) (contrib, expert float64) {
	contrib, expert = DefaultContributorPoints, DefaultExpertPoints
	if v, ok := rw.TierPoints[string(store.StandingTierDomainContributor)]; ok && v > 0 {
		contrib = v
	}
	if v, ok := rw.TierPoints[string(store.StandingTierDomainExpert)]; ok && v > 0 {
		expert = v
	}
	return contrib, expert
}

// RecomputeStanding recomputes standing for userID (nil = nightly full pass
// over every user with activity, 500 at a time). domainID 0 = every domain.
func RecomputeStanding(ctx context.Context, d Deps, userID *uuid.UUID, domainID int16) error {
	if userID != nil {
		return recomputeStandingFor(ctx, d, []uuid.UUID{*userID}, domainID)
	}
	after := uuid.Nil
	for {
		ids, err := d.Store.ListStandingUserBatch(ctx, store.ListStandingUserBatchParams{AfterID: after, Lim: standingBatch})
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if err := recomputeStandingFor(ctx, d, ids, 0); err != nil {
			return err
		}
		if len(ids) < standingBatch {
			return nil
		}
		after = ids[len(ids)-1]
	}
}

type standingKey struct {
	user   uuid.UUID
	domain int16
}

func recomputeStandingFor(ctx context.Context, d Deps, users []uuid.UUID, domainID int16) error {
	rows, err := d.Store.ListStandingSignals(ctx, store.ListStandingSignalsParams{UserIds: users, DomainID: domainID})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	declaredIDs, err := d.Store.ListDeclaredHistoryUsers(ctx, users)
	if err != nil {
		return err
	}
	declared := make(map[uuid.UUID]bool, len(declaredIDs))
	for _, id := range declaredIDs {
		declared[id] = true
	}
	sigs := map[standingKey]*StandingSignals{}
	var order []standingKey
	for _, r := range rows {
		k := standingKey{r.UserID, r.DomainID}
		s, ok := sigs[k]
		if !ok {
			s = &StandingSignals{}
			sigs[k] = s
			order = append(order, k)
		}
		switch r.Kind {
		case "solved":
			s.Solved += r.N
		case "current_first":
			s.CurrentFirst += r.N
		case "current_later":
			s.CurrentLater += r.N
		case "top_solution":
			s.TopSolutions += r.N
		case "votes":
			s.Votes += r.N
		case "vouches":
			s.Vouches += r.N
		}
	}
	p := store.UpsertStandingsParams{Now: nowFn(d)()}
	for _, k := range order {
		pts := StandingPoints(*sigs[k])
		p.UserIds = append(p.UserIds, k.user)
		p.DomainIds = append(p.DomainIds, k.domain)
		p.Points = append(p.Points, pts)
		p.Tiers = append(p.Tiers, string(StandingTier(d.Cfg.RankWeights, pts, declared[k.user])))
	}
	return d.Store.UpsertStandings(ctx, p)
}

// standingUniquePeriod matches RecomputeStandingArgs' uniqueness window.
const standingUniquePeriod = 30 * time.Second

// EnqueueStanding enqueues RecomputeStanding for (userID, domainID) inside tx.
// Like the pick jobs, only unfinished jobs count as duplicates, and if the
// duplicate is already running (it may have read before this write) a
// follow-up is scheduled for the next window under its own unique key.
func EnqueueStanding(ctx context.Context, ins TxInserter, tx pgx.Tx, userID uuid.UUID, domainID int16) error {
	if ins == nil {
		return nil
	}
	args := RecomputeStandingArgs{UserID: &userID, DomainID: domainID}
	res, err := ins.InsertTx(ctx, tx, args, standingJobOpts(nil))
	if err != nil {
		return err
	}
	if res.UniqueSkippedAsDuplicate && res.Job != nil && res.Job.State == rivertype.JobStateRunning {
		at := time.Now().Truncate(standingUniquePeriod).Add(standingUniquePeriod)
		_, err = ins.InsertTx(ctx, tx, args, standingJobOpts(&at))
	}
	return err
}

func standingJobOpts(at *time.Time) *river.InsertOpts {
	o := &river.InsertOpts{UniqueOpts: river.UniqueOpts{
		ByArgs:   true,
		ByPeriod: standingUniquePeriod,
		ByQueue:  at != nil,
		ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
			rivertype.JobStateRetryable, rivertype.JobStateScheduled,
		},
	}}
	if at != nil {
		o.ScheduledAt = *at
	}
	return o
}
