package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// ClusterNightlyWorker is a stub; the owning phase implements it.
type ClusterNightlyWorker struct {
	river.WorkerDefaults[ClusterNightlyArgs]
	d Deps
}

func (w *ClusterNightlyWorker) Work(ctx context.Context, job *river.Job[ClusterNightlyArgs]) error {
	return nil
}
