package jobs

// The ranking jobs are exercised end to end with real data in
// internal/service (votes_test.go, revisions_test.go, solutions_test.go) and
// through River in internal/http (phase1_test.go, phase2_test.go). These
// tests cover the job-local pieces.

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
)

func TestSameCandidates(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	base := []ranking.Candidate{{ID: a, Score: 2, VoteCount: 2}, {ID: b, Score: 1, VoteCount: 1}}
	cases := []struct {
		name  string
		other []ranking.Candidate
		want  bool
	}{
		{"equal", []ranking.Candidate{{ID: a, Score: 2, VoteCount: 2}, {ID: b, Score: 1, VoteCount: 1}}, true},
		{"score moved", []ranking.Candidate{{ID: a, Score: 3, VoteCount: 3}, {ID: b, Score: 1, VoteCount: 1}}, false},
		{"order changed", []ranking.Candidate{{ID: b, Score: 1, VoteCount: 1}, {ID: a, Score: 2, VoteCount: 2}}, false},
		{"new revision", append(append([]ranking.Candidate{}, base...), ranking.Candidate{ID: uuid.New()}), false},
	}
	for _, tc := range cases {
		if got := sameCandidates(base, tc.other); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestPickJobsIgnoreMissingRows(t *testing.T) {
	st := storetest.Store(t)
	d := Deps{
		Store: st, Scorer: ranking.NewDecayScorer(720 * time.Hour),
		Cfg: config.Config{RankMinVotesToTakeOver: 3, SoftSolvedThreshold: 5},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx := context.Background()
	if err := PickCurrentRevision(ctx, d, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := PickCurrentSolutionRevision(ctx, d, nil, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err := RescoreAll(ctx, d, nil, RescoreAllArgs{}); err != nil {
		t.Fatal(err)
	}
	if err := RescoreAll(ctx, d, nil, RescoreAllArgs{ProblemIDs: []uuid.UUID{uuid.New()}}); err != nil {
		t.Fatal(err)
	}
	if nowFn(Deps{})().Location() != time.UTC {
		t.Fatal("default clock is not UTC")
	}
}
