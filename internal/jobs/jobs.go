package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// Deps is everything a worker may use.
type Deps struct {
	Store  *store.Store
	Scorer ranking.Scorer
	Cfg    config.Config
	Log    *slog.Logger
	Now    func() time.Time
}

// Workers registers every worker. Each worker lives in its own file.
func Workers(d Deps) *river.Workers {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	w := river.NewWorkers()
	river.AddWorker(w, &PickCurrentRevisionWorker{d: d})
	river.AddWorker(w, &PickCurrentSolutionRevisionWorker{d: d})
	river.AddWorker(w, &RescoreAllWorker{d: d})
	river.AddWorker(w, &SessionSweepWorker{d: d})
	river.AddWorker(w, &SolvedPromptWorker{d: d})
	river.AddWorker(w, &RecomputeStandingWorker{d: d})
	river.AddWorker(w, &EmbedRevisionWorker{d: d})
	river.AddWorker(w, &ClusterNightlyWorker{d: d})
	return w
}

// PeriodicJobs are the scheduled jobs. River runs them on the elected leader only.
func PeriodicJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{
		river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return SessionSweepArgs{}, nil },
			&river.PeriodicJobOpts{ID: "session_sweep"}),
		river.NewPeriodicJob(dailyAt(2, 0),
			func() (river.JobArgs, *river.InsertOpts) { return ClusterNightlyArgs{}, nil },
			&river.PeriodicJobOpts{ID: "cluster_nightly"}),
		river.NewPeriodicJob(dailyAt(3, 0),
			func() (river.JobArgs, *river.InsertOpts) { return RecomputeStandingArgs{}, nil },
			&river.PeriodicJobOpts{ID: "recompute_standing_nightly"}),
	}
}

// Config is the River client config for the server process: queue default,
// 10 workers per machine.
func Config(d Deps) *river.Config {
	return &river.Config{
		Queues:       map[string]river.QueueConfig{queue: {MaxWorkers: 10}},
		Workers:      Workers(d),
		PeriodicJobs: PeriodicJobs(),
		ErrorHandler: &errorHandler{log: d.Log},
		MaxAttempts:  maxAttempts,
		Logger:       d.Log,
	}
}

// errorHandler logs the final failure of a job (discarded after retries) at
// ERROR so the "any job discarded" alert fires.
type errorHandler struct{ log *slog.Logger }

func (h *errorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	if job.Attempt >= job.MaxAttempts {
		h.log.ErrorContext(ctx, "job discarded after retries", "kind", job.Kind, "job_id", job.ID, "attempt", job.Attempt, "err", err)
		sentry.CaptureException(fmt.Errorf("job %s (id %d) discarded after %d attempts: %w", job.Kind, job.ID, job.Attempt, err))
	} else {
		h.log.WarnContext(ctx, "job failed, will retry", "kind", job.Kind, "job_id", job.ID, "attempt", job.Attempt, "err", err)
	}
	return nil
}

func (h *errorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	h.log.ErrorContext(ctx, "job panicked", "kind", job.Kind, "job_id", job.ID, "attempt", job.Attempt, "panic", panicVal, "trace", trace)
	return nil
}

type dailySchedule struct{ hour, minute int }

func dailyAt(hour, minute int) river.PeriodicSchedule { return dailySchedule{hour, minute} }

func (s dailySchedule) Next(t time.Time) time.Time {
	t = t.UTC()
	n := time.Date(t.Year(), t.Month(), t.Day(), s.hour, s.minute, 0, 0, time.UTC)
	if !n.After(t) {
		n = n.Add(24 * time.Hour)
	}
	return n
}
