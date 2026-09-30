package jobs

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

const dupBase = "Our clinic receptionists phone every patient the day before to confirm appointments, " +
	"then write each answer into a paper diary and later retype cancellations into the booking system manually"

func setEmbedding(t *testing.T, st *store.Store, rid uuid.UUID, text string) {
	t.Helper()
	if err := st.SetRevisionEmbedding(context.Background(), store.SetRevisionEmbeddingParams{
		ID: rid, Embedding: VectorLiteral(fakeVec(text)),
	}); err != nil {
		t.Fatal(err)
	}
}

func vote(t *testing.T, pool *pgxpool.Pool, rid uuid.UUID, weight float32, at time.Time) {
	t.Helper()
	uid := uuid.Must(uuid.NewV7())
	insertUser(t, pool, uid)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO problem_revision_votes (revision_id, user_id, weight, created_at) VALUES ($1, $2, $3, $4)`,
		rid, uid, weight, at); err != nil {
		t.Fatal(err)
	}
}

type mergeRow struct {
	ID         uuid.UUID
	A, B       uuid.UUID
	Similarity float32
	Dismissed  bool
}

func mergeRows(t *testing.T, pool *pgxpool.Pool, ids []uuid.UUID) []mergeRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id, problem_a_id, problem_b_id, similarity, dismissed_at IS NOT NULL
		FROM merge_suggestions WHERE problem_a_id = ANY($1) OR problem_b_id = ANY($1)
		ORDER BY problem_a_id, problem_b_id`, ids)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []mergeRow
	for rows.Next() {
		var m mergeRow
		if err := rows.Scan(&m.ID, &m.A, &m.B, &m.Similarity, &m.Dismissed); err != nil {
			t.Fatal(err)
		}
		out = append(out, m)
	}
	return out
}

type trendRow struct {
	ProblemID uuid.UUID
	Cluster   int32
	Rank      int32
}

func trendRows(t *testing.T, pool *pgxpool.Pool) []trendRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT problem_id, cluster_id, rank FROM trending_problems ORDER BY rank`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []trendRow
	for rows.Next() {
		var r trendRow
		if err := rows.Scan(&r.ProblemID, &r.Cluster, &r.Rank); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func TestClusterNightly(t *testing.T) {
	ctx := context.Background()
	st := storetest.Store(t)
	pool := st.Pool
	now := time.Now().UTC()

	// Four near-duplicates (one extra word each) and three unrelated problems.
	var dupIDs, dupRevs []uuid.UUID
	for i, extra := range []string{"weekly", "daily", "often", "always"} {
		text := dupBase + " " + extra
		pid, rid := insertProblem(t, pool, fmt.Sprintf("Appointment confirmation calls %d", i), text+" and more words to pass the length check", "Takes the whole morning every day.")
		setEmbedding(t, st, rid, text)
		dupIDs, dupRevs = append(dupIDs, pid), append(dupRevs, rid)
	}
	cID, cRev := insertProblem(t, pool, "Timber offcuts never tracked on site", "Carpenters throw offcuts into skips and nobody records which lengths could be reused later on.", "We buy new timber we already had.")
	setEmbedding(t, st, cRev, "carpenters throw timber offcuts into skips nobody records lengths reuse buy new")
	dID, dRev := insertProblem(t, pool, "Volunteer rota lives in a group chat", "Coordinators post the volunteer rota in a messaging group and people reply with thumbs up emojis.", "Shifts get double booked or missed.")
	setEmbedding(t, st, dRev, "volunteer rota group chat thumbs emoji shifts double booked missed")
	invID, invRev := insertProblem(t, pool, "Appointment confirmation calls duplicate spam", dupBase+" spam spam spam and filler", "Takes the whole morning every day.")
	setEmbedding(t, st, invRev, dupBase+" weekly")
	if _, err := pool.Exec(ctx, `UPDATE problems SET state = 'invalid' WHERE id = $1`, invID); err != nil {
		t.Fatal(err)
	}

	// Recent weights: dup cluster 4+3+2+1 = 10, C = 5. D only has an old vote.
	for i, w := range []float32{4, 3, 2, 1} {
		vote(t, pool, dupRevs[i], w, now.Add(-time.Hour))
	}
	vote(t, pool, cRev, 5, now.Add(-2*24*time.Hour))
	vote(t, pool, dRev, 9, now.Add(-8*24*time.Hour))
	vote(t, pool, invRev, 50, now.Add(-time.Hour))

	w := &ClusterNightlyWorker{d: Deps{Store: st, Log: testLog(), Now: func() time.Time { return now }}}
	run := func() {
		t.Helper()
		if err := w.Work(ctx, &river.Job[ClusterNightlyArgs]{}); err != nil {
			t.Fatal(err)
		}
	}
	run()

	all := append(append([]uuid.UUID{}, dupIDs...), cID, dID, invID)
	merges := mergeRows(t, pool, all)
	if len(merges) != 6 { // C(4,2) pairs among the duplicates; nothing else
		t.Fatalf("want 6 merge suggestions, got %d: %+v", len(merges), merges)
	}
	isDup := map[uuid.UUID]bool{}
	for _, id := range dupIDs {
		isDup[id] = true
	}
	for _, m := range merges {
		if !isDup[m.A] || !isDup[m.B] || m.Similarity < mergeMinSimilarity || m.A.String() >= m.B.String() {
			t.Fatalf("unexpected suggestion %+v", m)
		}
	}

	trend := trendRows(t, pool)
	want := []trendRow{
		{dupIDs[0], 1, 1}, {dupIDs[1], 1, 2}, {dupIDs[2], 1, 3}, // capped at 3 per cluster
		{cID, 2, 4},
	}
	if !reflect.DeepEqual(trend, want) {
		t.Fatalf("trending:\n got %+v\nwant %+v", trend, want)
	}

	// Idempotent: a second run leaves identical rows.
	run()
	if got := mergeRows(t, pool, all); !reflect.DeepEqual(got, merges) {
		t.Fatalf("merge rows changed on re-run:\n%+v\n%+v", got, merges)
	}
	if got := trendRows(t, pool); !reflect.DeepEqual(got, want) {
		t.Fatalf("trending changed on re-run: %+v", got)
	}

	// Dismissed suggestions are never resurrected or updated.
	dismissed := merges[0].ID
	if _, err := pool.Exec(ctx, `UPDATE merge_suggestions SET dismissed_at = now(), similarity = 0.5 WHERE id = $1`, dismissed); err != nil {
		t.Fatal(err)
	}
	run()
	for _, m := range mergeRows(t, pool, all) {
		if m.ID == dismissed && (!m.Dismissed || m.Similarity != 0.5) {
			t.Fatalf("dismissed suggestion touched: %+v", m)
		}
	}
	if n := len(mergeRows(t, pool, all)); n != 6 {
		t.Fatalf("want still 6 rows, got %d", n)
	}
}

func TestRankTrending(t *testing.T) {
	id := func(b byte) uuid.UUID { return uuid.UUID{15: b} }
	x := []float32{1, 0}
	y := []float32{0, 1}
	items := []trendItem{
		{id(1), 1, x}, {id(2), 2, x}, {id(3), 6, y}, {id(4), 3, x}, {id(5), 0.5, x},
	}
	got := rankTrending(items, 0.75, 2, 3)
	// x cluster weighs 6.5, y cluster 6: x first, top two of x, then y.
	want := []trendPick{{id(4), 1, 1}, {id(2), 1, 2}, {id(3), 2, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if got := rankTrending(nil, 0.75, 3, 50); len(got) != 0 {
		t.Fatalf("empty input: %+v", got)
	}
}
