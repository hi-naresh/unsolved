package jobs

import (
	"context"
	"net/http"
	"testing"

	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/riverqueue/river"
)

const (
	testProcess = "Every morning the dispatcher copies the delivery notes from email into the warehouse spreadsheet by hand."
	testPain    = "It takes an hour and mistakes mean lorries leave with the wrong stock."
)

func TestEmbedRevisionStoresVector(t *testing.T) {
	ctx := context.Background()
	st := storetest.Store(t)
	ml := fakeML(t, 0, http.StatusOK)
	_, rid := insertProblem(t, st.Pool, "Delivery notes retyped into stock sheet", testProcess, testPain)

	w := &EmbedRevisionWorker{d: Deps{Store: st, Cfg: config.Config{MLURL: ml.URL}, Log: testLog()}}
	job := &river.Job[EmbedRevisionArgs]{Args: EmbedRevisionArgs{RevisionID: rid}}
	for range 2 { // idempotent
		if err := w.Work(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	var lit string
	if err := st.Pool.QueryRow(ctx, `SELECT embedding::text FROM problem_revisions WHERE id = $1`, rid).Scan(&lit); err != nil {
		t.Fatal(err)
	}
	got, err := ParseVector(lit)
	if err != nil {
		t.Fatal(err)
	}
	want := fakeVec(RevisionText("Delivery notes retyped into stock sheet", testProcess, testPain))
	if len(got) != EmbeddingDim || dot(got, want) < 0.9999 {
		t.Fatalf("stored vector differs: len %d, sim %f", len(got), dot(got, want))
	}
}

func TestEmbedRevisionDisabledIsNoop(t *testing.T) {
	ctx := context.Background()
	st := storetest.Store(t)
	_, rid := insertProblem(t, st.Pool, "Delivery notes retyped into stock sheet", testProcess, testPain)
	w := &EmbedRevisionWorker{d: Deps{Store: st, Log: testLog()}}
	if err := w.Work(ctx, &river.Job[EmbedRevisionArgs]{Args: EmbedRevisionArgs{RevisionID: rid}}); err != nil {
		t.Fatal(err)
	}
	var isNull bool
	_ = st.Pool.QueryRow(ctx, `SELECT embedding IS NULL FROM problem_revisions WHERE id = $1`, rid).Scan(&isNull)
	if !isNull {
		t.Fatal("embedding written with ML disabled")
	}
}

func TestEmbedRevisionMLErrorRetries(t *testing.T) {
	ctx := context.Background()
	st := storetest.Store(t)
	ml := fakeML(t, 0, http.StatusServiceUnavailable)
	_, rid := insertProblem(t, st.Pool, "Delivery notes retyped into stock sheet", testProcess, testPain)
	w := &EmbedRevisionWorker{d: Deps{Store: st, Cfg: config.Config{MLURL: ml.URL}, Log: testLog()}}
	if err := w.Work(ctx, &river.Job[EmbedRevisionArgs]{Args: EmbedRevisionArgs{RevisionID: rid}}); err == nil {
		t.Fatal("want error so River retries")
	}
}
