package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// EmbedRevisionWorker is a stub; the owning phase implements it.
type EmbedRevisionWorker struct {
	river.WorkerDefaults[EmbedRevisionArgs]
	d Deps
}

func (w *EmbedRevisionWorker) Work(ctx context.Context, job *river.Job[EmbedRevisionArgs]) error {
	return nil
}
