package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// RescoreAllWorker is a stub; the owning phase implements it.
type RescoreAllWorker struct {
	river.WorkerDefaults[RescoreAllArgs]
	d Deps
}

func (w *RescoreAllWorker) Work(ctx context.Context, job *river.Job[RescoreAllArgs]) error {
	return nil
}
