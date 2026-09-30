package ranking

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDecayHalvesAtOneHalfLife(t *testing.T) {
	s := NewDecayScorer(720 * time.Hour)
	cast := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	stored := s.Contribution(1, cast)
	tests := []struct {
		name string
		at   time.Time
		want float64
	}{
		{"at cast", cast, 1},
		{"one half-life", cast.Add(720 * time.Hour), 0.5},
		{"two half-lives", cast.Add(1440 * time.Hour), 0.25},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.Display(stored, tc.at); math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("Display = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestContributionAtEpochIsWeight(t *testing.T) {
	s := NewDecayScorer(720 * time.Hour)
	if got := s.Contribution(2.5, Epoch); got != 2.5 {
		t.Fatalf("Contribution at epoch = %v, want 2.5", got)
	}
}

func TestOrderingMatchesDecayedOrdering(t *testing.T) {
	s := NewDecayScorer(720 * time.Hour)
	early := s.Contribution(3, Epoch.Add(10*24*time.Hour))
	late := s.Contribution(1, Epoch.Add(60*24*time.Hour))
	now := Epoch.Add(90 * 24 * time.Hour)
	if (late > early) != (s.Display(late, now) > s.Display(early, now)) {
		t.Fatal("stored ordering differs from decayed ordering")
	}
}

func TestFlatWeigher(t *testing.T) {
	w, err := FlatWeigher{}.Weight(context.Background(), uuid.New(), 1)
	if err != nil || w != 1 {
		t.Fatalf("got %v, %v", w, err)
	}
}

func TestPickLeader(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	tests := []struct {
		name      string
		cands     []Candidate
		incumbent uuid.UUID
		want      uuid.UUID
	}{
		{"tie keeps incumbent", []Candidate{{b, 5, 5}, {a, 5, 5}}, a, a},
		{"takeover blocked under minimum votes", []Candidate{{b, 9, 2}, {a, 5, 5}}, a, a},
		{"takeover at minimum votes", []Candidate{{b, 9, 3}, {a, 5, 5}}, a, b},
		{"second challenger qualifies", []Candidate{{b, 9, 1}, {c, 7, 4}, {a, 5, 5}}, a, c},
		{"incumbent already leads", []Candidate{{a, 9, 1}, {b, 7, 4}}, a, a},
		{"no incumbent picks top", []Candidate{{b, 1, 0}, {a, 0, 0}}, uuid.Nil, b},
		{"empty keeps incumbent", nil, a, a},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PickLeader(tc.cands, tc.incumbent, 3); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
