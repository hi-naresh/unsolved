package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store"
)

func TestTierWeight(t *testing.T) {
	full := config.RankWeights{
		Tiers:            map[string]float64{"member": 1, "declared_background": 9, "domain_contributor": 2, "domain_expert": 4},
		SuspiciousFactor: 0.25,
		NewAccountHours:  72,
	}
	old := 30 * 24 * time.Hour
	cases := []struct {
		name      string
		rw        config.RankWeights
		tier      string
		age       time.Duration
		suspended bool
		want      float64
	}{
		{"empty config member", config.RankWeights{}, "member", old, false, 1},
		{"empty config expert", config.RankWeights{}, "domain_expert", old, false, 1},
		{"empty config new suspended", config.RankWeights{}, "domain_expert", time.Minute, true, 1},
		{"member", full, "member", old, false, 1},
		{"contributor", full, "domain_contributor", old, false, 2},
		{"expert", full, "domain_expert", old, false, 4},
		{"declared background weighs as member", full, "declared_background", old, false, 1},
		{"unknown tier", full, "wizard", old, false, 1},
		{"missing entry falls back to 1", config.RankWeights{Tiers: map[string]float64{"domain_expert": 3}}, "domain_contributor", old, false, 1},
		{"member configured", config.RankWeights{Tiers: map[string]float64{"member": 0.5}}, "member", old, false, 0.5},
		{"new account", full, "domain_expert", time.Hour, false, 1},
		{"suspended", full, "domain_contributor", old, true, 0.5},
		{"new account member", full, "member", 71 * time.Hour, false, 0.25},
		{"exactly new-account age is not new", full, "member", 72 * time.Hour, false, 1},
		{"factor zero is off", config.RankWeights{Tiers: full.Tiers, NewAccountHours: 72}, "domain_expert", time.Hour, true, 4},
		{"no new-account window", config.RankWeights{SuspiciousFactor: 0.5}, "member", time.Minute, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TierWeight(tc.rw, tc.tier, tc.age, tc.suspended); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewWeigherEmptyConfigIsFlat(t *testing.T) {
	w := NewWeigher(nil, config.Config{})
	if _, ok := w.(ranking.FlatWeigher); !ok {
		t.Fatalf("empty RANK_WEIGHTS_JSON: got %T, want FlatWeigher", w)
	}
	got, err := w.Weight(context.Background(), uuid.New(), 1)
	if err != nil || got != 1 {
		t.Fatalf("weight %v err %v", got, err)
	}
	w = NewWeigher(nil, config.Config{RankWeightsJSON: `{"tiers":{"domain_expert":3}}`,
		RankWeights: config.RankWeights{Tiers: map[string]float64{"domain_expert": 3}}})
	if _, ok := w.(*TierWeigher); !ok {
		t.Fatalf("with config: got %T", w)
	}
}

func TestCanVoteState(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-13 * 24 * time.Hour)
	silent := now.Add(-PosterSilence)
	open, solved, invalid := store.ProblemStateOpen, store.ProblemStateSolved, store.ProblemStateInvalid
	cases := []struct {
		name     string
		to, from store.ProblemState
		tier     string
		last     time.Time
		want     bool
	}{
		{"member cannot vote invalid", invalid, open, "member", silent, false},
		{"declared background cannot vote invalid", invalid, open, "declared_background", silent, false},
		{"contributor votes invalid", invalid, open, "domain_contributor", fresh, true},
		{"expert votes invalid on solved", invalid, solved, "domain_expert", fresh, true},
		{"nothing on invalid", invalid, invalid, "domain_expert", fresh, false},
		{"solved blocked before 14 days", solved, open, "domain_expert", fresh, false},
		{"solved allowed at 14 days", solved, open, "member", silent, true},
		{"solved only from open", solved, solved, "member", silent, false},
		{"open is not a community vote", open, solved, "domain_expert", silent, false},
	}
	for _, tc := range cases {
		if got := CanVoteState(tc.to, tc.from, tc.tier, tc.last, now); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestVotePercent(t *testing.T) {
	for _, tc := range []struct {
		sum, th float64
		want    int
	}{{0, 10, 0}, {4, 10, 40}, {9.99, 10, 99}, {10, 10, 100}, {25, 10, 100}, {-1, 10, 0}} {
		if got := VotePercent(tc.sum, tc.th); got != tc.want {
			t.Errorf("VotePercent(%v, %v) = %d, want %d", tc.sum, tc.th, got, tc.want)
		}
	}
}
