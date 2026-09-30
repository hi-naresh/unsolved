// Package jobs holds River job args and workers. Args live here (not in
// service) so services can enqueue with river.InsertTx without an import cycle;
// workers use the store directly and never import service.
package jobs

import (
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

const (
	queue       = river.QueueDefault
	maxAttempts = 5
)

func defaultOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queue, MaxAttempts: maxAttempts}
}

// PickCurrentRevisionArgs: enqueued by every problem-revision vote; unique per
// problem over 5 s so a burst collapses to one job.
type PickCurrentRevisionArgs struct {
	ProblemID uuid.UUID `json:"problem_id" river:"unique"`
}

func (PickCurrentRevisionArgs) Kind() string { return "pick_current_revision" }
func (PickCurrentRevisionArgs) InsertOpts() river.InsertOpts {
	o := defaultOpts()
	o.UniqueOpts = river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Second}
	return o
}

// PickCurrentSolutionRevisionArgs: enqueued by every solution-revision vote;
// unique per solution over 5 s.
type PickCurrentSolutionRevisionArgs struct {
	SolutionID uuid.UUID `json:"solution_id" river:"unique"`
}

func (PickCurrentSolutionRevisionArgs) Kind() string { return "pick_current_solution_revision" }
func (PickCurrentSolutionRevisionArgs) InsertOpts() river.InsertOpts {
	o := defaultOpts()
	o.UniqueOpts = river.UniqueOpts{ByArgs: true, ByPeriod: 5 * time.Second}
	return o
}

// RescoreAllArgs: manual (admin). Rebuilds every stored score from the vote
// tables, 500 rows per batch. ProblemIDs narrows the rebuild to those
// problems (used after an admin zeroes a user's vote weights); empty = all.
type RescoreAllArgs struct {
	ProblemIDs []uuid.UUID `json:"problem_ids,omitempty"`
}

func (RescoreAllArgs) Kind() string                 { return "rescore_all" }
func (RescoreAllArgs) InsertOpts() river.InsertOpts { return defaultOpts() }

// SessionSweepArgs: periodic, hourly. Deletes expired sessions.
type SessionSweepArgs struct{}

func (SessionSweepArgs) Kind() string                 { return "session_sweep" }
func (SessionSweepArgs) InsertOpts() river.InsertOpts { return defaultOpts() }

// SolvedPromptArgs (phase 3): the problem's top solution first crossed
// SOFT_SOLVED_THRESHOLD; email the poster a one-tap outcome link.
type SolvedPromptArgs struct {
	ProblemID  uuid.UUID `json:"problem_id" river:"unique"`
	SolutionID uuid.UUID `json:"solution_id" river:"unique"`
}

func (SolvedPromptArgs) Kind() string { return "solved_prompt" }
func (SolvedPromptArgs) InsertOpts() river.InsertOpts {
	o := defaultOpts()
	o.UniqueOpts = river.UniqueOpts{ByArgs: true}
	return o
}

// RecomputeStandingArgs (phase 4): update user_domain_standing. UserID nil =
// nightly full pass.
type RecomputeStandingArgs struct {
	UserID   *uuid.UUID `json:"user_id,omitempty" river:"unique"`
	DomainID int16      `json:"domain_id,omitempty" river:"unique"`
}

func (RecomputeStandingArgs) Kind() string { return "recompute_standing" }
func (RecomputeStandingArgs) InsertOpts() river.InsertOpts {
	o := defaultOpts()
	o.UniqueOpts = river.UniqueOpts{ByArgs: true, ByPeriod: 30 * time.Second}
	return o
}

// EmbedRevisionArgs (phase 5): a new problem revision was written.
type EmbedRevisionArgs struct {
	RevisionID uuid.UUID `json:"revision_id" river:"unique"`
}

func (EmbedRevisionArgs) Kind() string { return "embed_revision" }
func (EmbedRevisionArgs) InsertOpts() river.InsertOpts {
	o := defaultOpts()
	o.UniqueOpts = river.UniqueOpts{ByArgs: true}
	return o
}

// ClusterNightlyArgs (phase 5): periodic, 02:00 UTC.
type ClusterNightlyArgs struct{}

func (ClusterNightlyArgs) Kind() string                 { return "cluster_nightly" }
func (ClusterNightlyArgs) InsertOpts() river.InsertOpts { return defaultOpts() }
