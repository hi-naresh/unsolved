package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// RecomputeStandingWorker is a stub; the owning phase implements it.
type RecomputeStandingWorker struct {
	river.WorkerDefaults[RecomputeStandingArgs]
	d Deps
}

func (w *RecomputeStandingWorker) Work(ctx context.Context, job *river.Job[RecomputeStandingArgs]) error {
	return nil
}
