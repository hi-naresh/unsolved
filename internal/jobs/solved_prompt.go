package jobs

import (
	"context"

	"github.com/riverqueue/river"
)

// SolvedPromptWorker is a stub; the owning phase implements it.
type SolvedPromptWorker struct {
	river.WorkerDefaults[SolvedPromptArgs]
	d Deps
}

func (w *SolvedPromptWorker) Work(ctx context.Context, job *river.Job[SolvedPromptArgs]) error {
	return nil
}
