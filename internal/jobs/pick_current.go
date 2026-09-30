package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// PickCurrentRevisionWorker is a stub; the owning phase implements it.
type PickCurrentRevisionWorker struct {
	river.WorkerDefaults[PickCurrentRevisionArgs]
	d Deps
}

func (w *PickCurrentRevisionWorker) Work(ctx context.Context, job *river.Job[PickCurrentRevisionArgs]) error {
	return nil
}

// PickCurrentSolutionRevisionWorker is a stub; the owning phase implements it.
type PickCurrentSolutionRevisionWorker struct {
	river.WorkerDefaults[PickCurrentSolutionRevisionArgs]
	d Deps
}

func (w *PickCurrentSolutionRevisionWorker) Work(ctx context.Context, job *river.Job[PickCurrentSolutionRevisionArgs]) error {
	return nil
}
