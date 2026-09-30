package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// SessionSweepWorker deletes expired sessions. Periodic, hourly; idempotent.
type SessionSweepWorker struct {
	river.WorkerDefaults[SessionSweepArgs]
	d Deps
}

func (w *SessionSweepWorker) Work(ctx context.Context, job *river.Job[SessionSweepArgs]) error {
	n, err := w.d.Store.DeleteExpiredSessions(ctx)
	if err != nil {
		return err
	}
	w.d.Log.InfoContext(ctx, "session sweep", "deleted", n)
	return nil
}
