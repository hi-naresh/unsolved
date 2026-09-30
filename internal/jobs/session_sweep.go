package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// SessionSweepWorker is a stub; the owning phase implements it.
type SessionSweepWorker struct {
	river.WorkerDefaults[SessionSweepArgs]
	d Deps
}

func (w *SessionSweepWorker) Work(ctx context.Context, job *river.Job[SessionSweepArgs]) error {
	return nil
}
