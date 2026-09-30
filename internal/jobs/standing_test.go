package jobs

import (
	"testing"

	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store"
)

func TestStandingPoints(t *testing.T) {
	cases := []struct {
		name string
		s    StandingSignals
		want float64
	}{
		{"nothing", StandingSignals{}, 0},
		{"solved problem", StandingSignals{Solved: 1}, 10},
		{"current later revision", StandingSignals{CurrentLater: 2}, 10},
		{"current first revision", StandingSignals{CurrentFirst: 1}, 2},
		{"top solution", StandingSignals{TopSolutions: 1}, 5},
		{"votes round down per 3", StandingSignals{Votes: 8}, 2},
		{"negative votes ignored", StandingSignals{Votes: -4}, 0},
		{"vouches", StandingSignals{Vouches: 2}, 6},
		{"everything", StandingSignals{Solved: 1, CurrentLater: 1, CurrentFirst: 1, TopSolutions: 1, Votes: 3, Vouches: 1}, 10 + 5 + 2 + 5 + 1 + 3},
	}
	for _, tc := range cases {
		if got := StandingPoints(tc.s); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStandingTier(t *testing.T) {
	custom := config.RankWeights{TierPoints: map[string]float64{"domain_contributor": 10, "domain_expert": 50}}
	cases := []struct {
		name     string
		rw       config.RankWeights
		points   float64
		declared bool
		want     store.StandingTier
	}{
		{"default member", config.RankWeights{}, 24, false, store.StandingTierMember},
		{"default contributor", config.RankWeights{}, 25, false, store.StandingTierDomainContributor},
		{"default expert", config.RankWeights{}, 100, true, store.StandingTierDomainExpert},
		{"declared below contributor", config.RankWeights{}, 0, true, store.StandingTierDeclaredBackground},
		{"declared does not beat points", config.RankWeights{}, 30, true, store.StandingTierDomainContributor},
		{"custom contributor", custom, 10, false, store.StandingTierDomainContributor},
		{"custom expert", custom, 50, false, store.StandingTierDomainExpert},
		{"zero threshold falls back", config.RankWeights{TierPoints: map[string]float64{"domain_contributor": 0}}, 10, false, store.StandingTierMember},
	}
	for _, tc := range cases {
		if got := StandingTier(tc.rw, tc.points, tc.declared); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
