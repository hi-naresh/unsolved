// Package ranking holds the public scoring rules. Scores are stored as
// epoch-scaled vote mass: each vote adds w·2^((t−t0)/h), so a vote is a
// constant-time column update and nothing ever re-decays a stored score.
package ranking

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"
)

// Epoch is t0, fixed forever. Changing it would invalidate every stored score.
var Epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type Scorer interface {
	// Contribution: what a vote of weight w cast at t adds to a stored score.
	Contribution(w float64, t time.Time) float64
	// Display: stored score converted to the decayed value at now.
	Display(stored float64, now time.Time) float64
}

type VoteWeigher interface {
	Weight(ctx context.Context, voterID uuid.UUID, domainID int16) (float64, error)
}

type DecayScorer struct {
	Epoch    time.Time     // fixed 2026-01-01T00:00:00Z, never changed
	HalfLife time.Duration // config RANK_HALF_LIFE
}

// NewDecayScorer returns a DecayScorer anchored at Epoch.
func NewDecayScorer(halfLife time.Duration) DecayScorer {
	return DecayScorer{Epoch: Epoch, HalfLife: halfLife}
}

func (s DecayScorer) halfLives(t time.Time) float64 {
	return float64(t.Sub(s.Epoch)) / float64(s.HalfLife)
}

func (s DecayScorer) Contribution(w float64, t time.Time) float64 {
	return w * math.Exp2(s.halfLives(t))
}

func (s DecayScorer) Display(stored float64, now time.Time) float64 {
	return stored / math.Exp2(s.halfLives(now))
}

// FlatWeigher is the launch VoteWeigher: every vote weighs 1.0.
type FlatWeigher struct{}

func (FlatWeigher) Weight(context.Context, uuid.UUID, int16) (float64, error) { return 1.0, nil }

// Candidate is a revision competing to be current.
type Candidate struct {
	ID        uuid.UUID
	Score     float64
	VoteCount int32
}

// PickLeader chooses the current revision. candidates must be ordered by
// stored score, highest first. The incumbent keeps its place on a tie, and a
// challenger needs at least minVotes votes to displace it. If the incumbent
// is not among candidates (or is uuid.Nil) the top candidate wins outright.
func PickLeader(candidates []Candidate, incumbent uuid.UUID, minVotes int32) uuid.UUID {
	if len(candidates) == 0 {
		return incumbent
	}
	var inc *Candidate
	for i := range candidates {
		if candidates[i].ID == incumbent {
			inc = &candidates[i]
			break
		}
	}
	if inc == nil {
		return candidates[0].ID
	}
	for _, c := range candidates {
		if c.ID == inc.ID {
			continue
		}
		if c.Score > inc.Score && c.VoteCount >= minVotes {
			return c.ID
		}
		if c.Score <= inc.Score {
			break // ordered by score: nobody further down can beat the incumbent
		}
	}
	return inc.ID
}
