package service

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store"
)

// NewWeigher returns the VoteWeigher. With no RANK_WEIGHTS_JSON every vote
// weighs 1.0 (the launch behaviour, unchanged); otherwise it is the tier
// weigher.
func NewWeigher(st *store.Store, cfg config.Config) ranking.VoteWeigher {
	rw := cfg.RankWeights
	if cfg.RankWeightsJSON == "" && len(rw.Tiers) == 0 && rw.SuspiciousFactor == 0 && rw.NewAccountHours == 0 {
		return ranking.FlatWeigher{}
	}
	return &TierWeigher{Store: st, Weights: rw, Now: func() time.Time { return time.Now().UTC() }}
}

// TierWeigher weighs a vote by the voter's standing tier in the content's
// domain (RankWeights.Tiers), down-weighted by SuspiciousFactor for new
// accounts and suspended voters. One indexed query per vote.
type TierWeigher struct {
	Store   *store.Store
	Weights config.RankWeights
	Now     func() time.Time
}

func (w *TierWeigher) Weight(ctx context.Context, voterID uuid.UUID, domainID int16) (float64, error) {
	v, err := w.Store.GetVoterStanding(ctx, store.GetVoterStandingParams{UserID: voterID, DomainID: domainID})
	if err != nil {
		return 0, notFound(err)
	}
	return TierWeight(w.Weights, v.Tier, w.Now().Sub(v.CreatedAt), v.Suspended), nil
}

// TierWeight is the weighing rule:
//   - weight = Tiers[tier]; a missing entry is 1.0, and member defaults to
//     1.0; declared_background always weighs what member does (declared
//     history carries no weight);
//   - suspicious voters (account younger than NewAccountHours, or suspended)
//     are multiplied by SuspiciousFactor (0 = unset = 1.0, i.e. off).
func TierWeight(rw config.RankWeights, tier string, accountAge time.Duration, suspended bool) float64 {
	lookup := func(t string) float64 {
		if v, ok := rw.Tiers[t]; ok && v >= 0 {
			return v
		}
		return 1.0
	}
	var w float64
	switch store.StandingTier(tier) {
	case store.StandingTierDomainContributor, store.StandingTierDomainExpert:
		w = lookup(tier)
	default: // member, declared_background, unknown
		w = lookup(string(store.StandingTierMember))
	}
	if rw.SuspiciousFactor > 0 {
		young := rw.NewAccountHours > 0 && accountAge < time.Duration(rw.NewAccountHours*float64(time.Hour))
		if young || suspended {
			w *= rw.SuspiciousFactor
		}
	}
	return w
}
