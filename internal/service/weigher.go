package service

import (
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store"
)

// NewWeigher returns the VoteWeigher. At launch every vote weighs 1.0; the
// tier-based weigher from RANK_WEIGHTS_JSON replaces this in phase 4.
func NewWeigher(_ *store.Store, _ config.Config) ranking.VoteWeigher {
	return ranking.FlatWeigher{}
}
