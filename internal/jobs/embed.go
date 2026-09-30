package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// EmbedRevisionWorker embeds a new problem revision (title + current process
// + pain) via the ML service and stores it in problem_revisions.embedding.
// Idempotent: re-running overwrites the same vector. With ML_URL empty it is a
// no-op; ML errors are returned so River retries (then logs at ERROR).
type EmbedRevisionWorker struct {
	river.WorkerDefaults[EmbedRevisionArgs]
	d Deps

	once     sync.Once
	ml       *MLClient
	disabled sync.Once // logs "ML disabled" once per process
}

func (w *EmbedRevisionWorker) client() *MLClient {
	w.once.Do(func() {
		if w.ml == nil { // tests may preset it
			w.ml = NewMLClient(w.d.Cfg.MLURL)
		}
	})
	return w.ml
}

func (w *EmbedRevisionWorker) Work(ctx context.Context, job *river.Job[EmbedRevisionArgs]) error {
	ml := w.client()
	if !ml.Enabled() {
		w.disabled.Do(func() {
			w.d.Log.InfoContext(ctx, "ML_URL not set; skipping revision embeddings")
		})
		return nil
	}
	rev, err := w.d.Store.GetRevisionText(ctx, job.Args.RevisionID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The enqueuing tx committed, so this only happens if the row is gone
		// for good; retrying won't help.
		return river.JobCancel(fmt.Errorf("revision %s not found", job.Args.RevisionID))
	}
	if err != nil {
		return err
	}
	vecs, err := ml.Embed(ctx, []string{RevisionText(rev.Title, rev.CurrentProcess, rev.Pain)})
	if err != nil {
		return err
	}
	return w.d.Store.SetRevisionEmbedding(ctx, store.SetRevisionEmbeddingParams{
		ID:        rev.ID,
		Embedding: VectorLiteral(vecs[0]),
	})
}
