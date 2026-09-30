package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/config"
	"github.com/hi-naresh/unsolved/internal/jobs"
	"github.com/hi-naresh/unsolved/internal/ranking"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/hi-naresh/unsolved/internal/store/storetest"
)

// discFakeVec: normalised bag of hashed words (same scheme as the jobs tests).
func discFakeVec(text string) []float32 {
	v := make([]float32, jobs.EmbeddingDim)
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%jobs.EmbeddingDim]++
	}
	var n float64
	for _, f := range v {
		n += float64(f * f)
	}
	if n == 0 {
		v[0], n = 1, 1
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(n))
	}
	return v
}

func discFakeML(t *testing.T, delay time.Duration, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
			return
		}
		if status != http.StatusOK {
			http.Error(w, "boom", status)
			return
		}
		var in struct{ Texts []string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		out := make([][]float32, len(in.Texts))
		for i, s := range in.Texts {
			out[i] = discFakeVec(s)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"vectors": out})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func discService(t *testing.T, mlURL string) *Service {
	t.Helper()
	st := storetest.Store(t)
	cfg := config.Config{MLURL: mlURL, RankHalfLife: 720 * time.Hour}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(st, nil, ranking.NewDecayScorer(cfg.RankHalfLife), ranking.FlatWeigher{}, cfg, log)
}

// discProblem inserts a user + problem + embedded current revision (test
// setup only) and returns the problem id and the author's handle.
func discProblem(t *testing.T, st *store.Store, display, title, process, pain string) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	uid, pid, rid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	handle := "d_" + strings.ReplaceAll(uid.String(), "-", "")[20:]
	if _, err := st.Pool.Exec(ctx, `INSERT INTO users (id, handle, display_name) VALUES ($1, $2, 'T')`, uid, handle); err != nil {
		t.Fatal(err)
	}
	_, err := st.Pool.Exec(ctx, `
		WITH p AS (
		  INSERT INTO problems (id, domain_id, author_id, author_display, current_revision_id)
		  VALUES ($1, 3, $2, $7, $3)
		)
		INSERT INTO problem_revisions (id, problem_id, author_id, author_display, title, current_process, pain, tried)
		VALUES ($3, $1, $2, $7, $4, $5, $6, 'nothing yet')`,
		pid, uid, rid, title, process, pain, display)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRevisionEmbedding(ctx, store.SetRevisionEmbeddingParams{
		ID: rid, Embedding: jobs.VectorLiteral(discFakeVec(jobs.RevisionText(title, process, pain))),
	}); err != nil {
		t.Fatal(err)
	}
	return pid, handle
}

const (
	pallets = "Forklift drivers write pallet locations on a whiteboard and the night shift photographs it"
	palletP = "Pallets go missing between shifts and we spend hours searching the warehouse racks."
)

func TestSearch(t *testing.T) {
	ctx := context.Background()
	s := discService(t, discFakeML(t, 0, http.StatusOK))
	want, _ := discProblem(t, s.Store, "anonymous", "Pallet locations kept on a whiteboard", pallets, palletP)
	other, _ := discProblem(t, s.Store, "named", "Bakery orders taken on paper slips",
		"Customers phone in cake orders and staff scribble them on paper slips pinned to a corkboard.", "Slips fall off and orders are forgotten.")
	invalid, _ := discProblem(t, s.Store, "named", "Pallet locations kept on a whiteboard again", pallets, palletP)
	if _, err := s.Store.Pool.Exec(ctx, `UPDATE problems SET state = 'invalid' WHERE id = $1`, invalid); err != nil {
		t.Fatal(err)
	}

	res, err := s.Search(ctx, "  pallet locations whiteboard night shift  ")
	if err != nil || res.Unavailable {
		t.Fatalf("search: %+v %v", res, err)
	}
	if res.Query != "pallet locations whiteboard night shift" || len(res.Items) == 0 || res.Items[0].ProblemID != want {
		t.Fatalf("want %s first, got %+v", want, res.Items)
	}
	top := res.Items[0]
	if top.AuthorHandle != "" || top.AuthorDisplay != store.DisplayModeAnonymous || top.DomainName != "Logistics" || top.AuthorTier != "member" {
		t.Fatalf("anonymous row leaked or incomplete: %+v", top)
	}
	for _, it := range res.Items {
		if it.ProblemID == invalid {
			t.Fatal("invalid problem returned")
		}
		if it.ProblemID == other && it.Similarity >= top.Similarity {
			t.Fatal("unrelated problem ranked above the match")
		}
	}

	if res, err := s.Search(ctx, "   "); err != nil || res.Unavailable || res.Items != nil {
		t.Fatalf("empty query: %+v %v", res, err)
	}
}

func TestSearchFailsOpen(t *testing.T) {
	ctx := context.Background()
	for name, url := range map[string]string{
		"disabled": "",
		"500":      discFakeML(t, 0, http.StatusInternalServerError),
		"timeout":  discFakeML(t, 3*time.Second, http.StatusOK),
	} {
		t.Run(name, func(t *testing.T) {
			s := discService(t, url)
			start := time.Now()
			res, err := s.Search(ctx, "pallet locations")
			if err != nil || !res.Unavailable || len(res.Items) != 0 {
				t.Fatalf("want unavailable, got %+v %v", res, err)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("took %v; the 1.5 s ML timeout didn't hold", d)
			}
			items, err := s.Similar(ctx, "Pallet locations on a whiteboard", pallets, palletP)
			if err != nil || items != nil {
				t.Fatalf("similar should fail open: %+v %v", items, err)
			}
		})
	}
}

func TestSimilar(t *testing.T) {
	ctx := context.Background()
	s := discService(t, discFakeML(t, 0, http.StatusOK))
	want, handle := discProblem(t, s.Store, "named", "Pallet locations on the whiteboard", pallets, palletP)

	items, err := s.Similar(ctx, "Pallet locations on the whiteboard", pallets+" every day", palletP)
	if err != nil || len(items) == 0 || items[0].ProblemID != want || items[0].AuthorHandle != handle {
		t.Fatalf("similar: %+v %v", items, err)
	}
	if len(items) > 5 {
		t.Fatalf("more than 5: %d", len(items))
	}
	if items, err := s.Similar(ctx, "hi", "short", ""); err != nil || items != nil {
		t.Fatalf("short input should skip ML: %+v %v", items, err)
	}
	items, err = s.Similar(ctx, "Choir music sheets", "The conductor photocopies sheet music for every singer before each rehearsal session.", "Paper everywhere.")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ProblemID == want {
			t.Fatal("unrelated text matched above threshold")
		}
	}
}

func TestTrendingAndMerges(t *testing.T) {
	ctx := context.Background()
	s := discService(t, "")
	var ids []uuid.UUID
	for i := range 9 {
		id, _ := discProblem(t, s.Store, "named", fmt.Sprintf("Merge candidate problem %d", i), pallets, palletP)
		ids = append(ids, id)
	}
	// Trending reads trending_problems in rank order.
	if _, err := s.Store.Pool.Exec(ctx, `DELETE FROM trending_problems`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.Pool.Exec(ctx, `INSERT INTO trending_problems (problem_id, cluster_id, rank) VALUES ($1, 1, 2), ($2, 1, 1)`, ids[0], ids[1]); err != nil {
		t.Fatal(err)
	}
	tr, err := s.Trending(ctx)
	if err != nil || len(tr) != 2 || tr[0].ProblemID != ids[1] || tr[1].ProblemID != ids[0] {
		t.Fatalf("trending: %+v %v", tr, err)
	}

	// 36 suggestions → two pages (30 + 6), most similar first.
	n := 0
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			a, b := ids[i], ids[j]
			if a.String() > b.String() {
				a, b = b, a
			}
			n++
			if _, err := s.Store.Pool.Exec(ctx, `INSERT INTO merge_suggestions (id, problem_a_id, problem_b_id, similarity) VALUES ($1, $2, $3, $4)`,
				uuid.Must(uuid.NewV7()), a, b, 0.85+float32(n)/1000); err != nil {
				t.Fatal(err)
			}
		}
	}
	p1, err := s.MergeSuggestions(ctx, "")
	if err != nil || len(p1.Items) != 30 || p1.Next == "" {
		t.Fatalf("page 1: %d items next=%q err=%v", len(p1.Items), p1.Next, err)
	}
	p2, err := s.MergeSuggestions(ctx, p1.Next)
	if err != nil || len(p2.Items) != 6 || p2.Next != "" {
		t.Fatalf("page 2: %d items next=%q err=%v", len(p2.Items), p2.Next, err)
	}
	if p1.Items[0].Similarity < p1.Items[1].Similarity || p2.Items[0].Similarity > p1.Items[29].Similarity {
		t.Fatal("not ordered by similarity")
	}
	if _, err := s.MergeSuggestions(ctx, "garbage"); !errors.As(err, new(ErrValidation)) {
		t.Fatalf("bad cursor: %v", err)
	}

	admin := uuid.Must(uuid.NewV7())
	if err := s.DismissMergeSuggestion(ctx, admin, p1.Items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DismissMergeSuggestion(ctx, admin, uuid.Must(uuid.NewV7())); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}
	p1b, _ := s.MergeSuggestions(ctx, "")
	if p1b.Items[0].ID == p1.Items[0].ID {
		t.Fatal("dismissed suggestion still listed")
	}
}
