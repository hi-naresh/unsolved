package jobs

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/hi-naresh/unsolved/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Tuning for ClusterNightly. Internal constants, not config.
const (
	clusterMaxProblems    = 20_000 // most recent problems considered
	clusterNeighbourBatch = 200    // problems per nearest-neighbour query
	mergeMinSimilarity    = 0.85   // cosine similarity for a merge suggestion
	trendClusterSim       = 0.75   // cosine similarity to join a trending cluster
	trendWindow           = 7 * 24 * time.Hour
	trendMax              = 50 // rows in trending_problems
	trendPerCluster       = 3  // at most this many problems per cluster
	mergeUpsertBatch      = 1000
)

// ClusterNightlyWorker runs at 02:00 UTC over stored embeddings (it never
// calls the ML service). It upserts merge suggestions for near-duplicate
// problems and rebuilds trending_problems. Idempotent: a re-run writes the
// same rows.
type ClusterNightlyWorker struct {
	river.WorkerDefaults[ClusterNightlyArgs]
	d Deps
}

func (w *ClusterNightlyWorker) Timeout(*river.Job[ClusterNightlyArgs]) time.Duration {
	return 30 * time.Minute
}

func (w *ClusterNightlyWorker) Work(ctx context.Context, job *river.Job[ClusterNightlyArgs]) error {
	start := time.Now()
	ids, err := w.d.Store.ListClusterCandidates(ctx, clusterMaxProblems)
	if err != nil {
		return fmt.Errorf("list candidates: %w", err)
	}
	pairs, err := w.mergeSuggestions(ctx, ids)
	if err != nil {
		return err
	}
	trending, err := w.trending(ctx, ids)
	if err != nil {
		return err
	}
	w.d.Log.InfoContext(ctx, "cluster nightly done", "problems", len(ids), "merge_pairs", pairs,
		"trending", trending, "dur_ms", time.Since(start).Milliseconds())
	return nil
}

type problemPair struct{ a, b uuid.UUID }

// mergeSuggestions finds each candidate's nearest neighbours in SQL (pgvector
// cosine distance) and upserts every pair at or above mergeMinSimilarity.
func (w *ClusterNightlyWorker) mergeSuggestions(ctx context.Context, ids []uuid.UUID) (int, error) {
	best := map[problemPair]float32{}
	for i := 0; i < len(ids); i += clusterNeighbourBatch {
		batch := ids[i:min(i+clusterNeighbourBatch, len(ids))]
		rows, err := w.d.Store.NearestNeighbours(ctx, store.NearestNeighboursParams{
			Ids: batch, MinSimilarity: mergeMinSimilarity,
		})
		if err != nil {
			return 0, fmt.Errorf("nearest neighbours: %w", err)
		}
		for _, r := range rows {
			p := orderedPair(r.ProblemID, r.NeighbourID)
			if s, ok := best[p]; !ok || r.Similarity > s {
				best[p] = r.Similarity
			}
		}
	}
	pairs := make([]problemPair, 0, len(best))
	for p := range best {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if c := bytes.Compare(pairs[i].a[:], pairs[j].a[:]); c != 0 {
			return c < 0
		}
		return bytes.Compare(pairs[i].b[:], pairs[j].b[:]) < 0
	})
	for i := 0; i < len(pairs); i += mergeUpsertBatch {
		chunk := pairs[i:min(i+mergeUpsertBatch, len(pairs))]
		arg := store.UpsertMergeSuggestionsParams{
			Ids:          make([]uuid.UUID, len(chunk)),
			AIds:         make([]uuid.UUID, len(chunk)),
			BIds:         make([]uuid.UUID, len(chunk)),
			Similarities: make([]float32, len(chunk)),
		}
		for j, p := range chunk {
			id, err := uuid.NewV7()
			if err != nil {
				return 0, err
			}
			arg.Ids[j], arg.AIds[j], arg.BIds[j], arg.Similarities[j] = id, p.a, p.b, best[p]
		}
		if err := w.d.Store.UpsertMergeSuggestions(ctx, arg); err != nil {
			return 0, fmt.Errorf("upsert merge suggestions: %w", err)
		}
	}
	return len(pairs), nil
}

func orderedPair(x, y uuid.UUID) problemPair {
	if bytes.Compare(x[:], y[:]) < 0 {
		return problemPair{x, y}
	}
	return problemPair{y, x}
}

// trendItem is one problem that received votes in the trend window.
type trendItem struct {
	id     uuid.UUID
	weight float64
	vec    []float32
}

// trending clusters the recently-voted candidates and replaces
// trending_problems with the top problems of the hottest clusters.
func (w *ClusterNightlyWorker) trending(ctx context.Context, ids []uuid.UUID) (int, error) {
	now := w.d.Now()
	rows, err := w.d.Store.RecentVoteWeightsWithEmbeddings(ctx, store.RecentVoteWeightsWithEmbeddingsParams{
		Since: now.Add(-trendWindow), Ids: ids,
	})
	if err != nil {
		return 0, fmt.Errorf("recent vote weights: %w", err)
	}
	items := make([]trendItem, 0, len(rows))
	for _, r := range rows {
		v, err := ParseVector(r.Embedding)
		if err != nil {
			return 0, fmt.Errorf("problem %s: %w", r.ProblemID, err)
		}
		items = append(items, trendItem{id: r.ProblemID, weight: r.Weight, vec: v})
	}
	picked := rankTrending(items, trendClusterSim, trendPerCluster, trendMax)

	arg := store.InsertTrendingParams{ComputedAt: now}
	for _, p := range picked {
		arg.ProblemIds = append(arg.ProblemIds, p.id)
		arg.ClusterIds = append(arg.ClusterIds, p.cluster)
		arg.Ranks = append(arg.Ranks, p.rank)
	}
	err = w.d.Store.InTx(ctx, func(q *store.Queries, _ pgx.Tx) error {
		if err := q.ClearTrending(ctx); err != nil {
			return err
		}
		if len(picked) == 0 {
			return nil
		}
		return q.InsertTrending(ctx, arg)
	})
	if err != nil {
		return 0, fmt.Errorf("write trending: %w", err)
	}
	return len(picked), nil
}

type trendPick struct {
	id            uuid.UUID
	cluster, rank int32
}

// rankTrending greedily clusters items (each joins the first cluster whose
// seed is at least minSim similar, else seeds a new one), ranks clusters by
// summed weight and returns up to limit problems, perCluster from each,
// hottest cluster first. Deterministic for a given input set.
func rankTrending(items []trendItem, minSim float32, perCluster, limit int) []trendPick {
	// Heaviest first, so the hottest problem seeds each cluster.
	sort.Slice(items, func(i, j int) bool {
		if items[i].weight != items[j].weight {
			return items[i].weight > items[j].weight
		}
		return bytes.Compare(items[i].id[:], items[j].id[:]) < 0
	})
	type cluster struct {
		seed    []float32
		members []trendItem // stays sorted: items arrive heaviest first
		weight  float64
	}
	var clusters []*cluster
	for _, it := range items {
		var home *cluster
		for _, c := range clusters {
			if dot(c.seed, it.vec) >= minSim {
				home = c
				break
			}
		}
		if home == nil {
			home = &cluster{seed: it.vec}
			clusters = append(clusters, home)
		}
		home.members = append(home.members, it)
		home.weight += it.weight
	}
	// Stable: equal-weight clusters keep seed order (itself deterministic).
	sort.SliceStable(clusters, func(i, j int) bool { return clusters[i].weight > clusters[j].weight })

	var out []trendPick
	for ci, c := range clusters {
		for k, m := range c.members {
			if k == perCluster || len(out) == limit {
				break
			}
			out = append(out, trendPick{id: m.id, cluster: int32(ci + 1), rank: int32(len(out) + 1)})
		}
		if len(out) == limit {
			break
		}
	}
	return out
}

// dot is cosine similarity for the normalised vectors the ML service returns.
func dot(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
