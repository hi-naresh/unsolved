-- Phase 5: embeddings, semantic search, duplicate check, nightly clustering.
-- Vectors cross the Go boundary as pgvector text literals ('[0.1,0.2,...]').

-- name: GetRevisionText :one
SELECT id, title, current_process, pain
FROM problem_revisions
WHERE id = $1;

-- name: SetRevisionEmbedding :exec
UPDATE problem_revisions
SET embedding = sqlc.arg(embedding)::text::vector
WHERE id = sqlc.arg(id);

-- name: SearchProblemsByEmbedding :many
-- Nearest current revisions of non-invalid problems to the query vector.
-- Exact scan (no ANN index at launch); fine at this size, never on page renders.
SELECT p.id AS problem_id, p.state, p.soft_solved, p.author_display,
       r.title, d.slug AS domain_slug, d.name AS domain_name,
       u.handle AS author_handle, (u.deleted_at IS NOT NULL)::boolean AS author_deleted,
       COALESCE(s.tier, 'member')::text AS author_tier,
       (1 - (r.embedding <=> sqlc.arg(embedding)::text::vector))::real AS similarity
FROM problems p
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN domains d ON d.id = p.domain_id
JOIN users u ON u.id = p.author_id
LEFT JOIN user_domain_standing s ON s.user_id = p.author_id AND s.domain_id = p.domain_id
WHERE p.state <> 'invalid' AND r.embedding IS NOT NULL
ORDER BY r.embedding <=> sqlc.arg(embedding)::text::vector, p.id
LIMIT sqlc.arg(lim);

-- name: ListClusterCandidates :many
-- The problems ClusterNightly considers: non-invalid, current revision
-- embedded, most recent first, capped.
SELECT p.id
FROM problems p
JOIN problem_revisions r ON r.id = p.current_revision_id
WHERE p.state <> 'invalid' AND r.embedding IS NOT NULL
ORDER BY p.created_at DESC, p.id
LIMIT sqlc.arg(lim);

-- name: NearestNeighbours :many
-- For each problem in the batch, its 5 nearest other non-invalid problems by
-- cosine distance of current-revision embeddings, kept if similar enough.
SELECT c.id AS problem_id, n.id AS neighbour_id, n.similarity
FROM (
  SELECT p.id, r.embedding
  FROM problems p
  JOIN problem_revisions r ON r.id = p.current_revision_id
  WHERE p.id = ANY(sqlc.arg(ids)::uuid[]) AND r.embedding IS NOT NULL
) c
CROSS JOIN LATERAL (
  SELECT p2.id, (1 - (r2.embedding <=> c.embedding))::real AS similarity
  FROM problems p2
  JOIN problem_revisions r2 ON r2.id = p2.current_revision_id
  WHERE p2.id <> c.id AND p2.state <> 'invalid' AND r2.embedding IS NOT NULL
  ORDER BY r2.embedding <=> c.embedding
  LIMIT 5
) n
WHERE n.similarity >= sqlc.arg(min_similarity)::real;

-- name: UpsertMergeSuggestions :exec
-- Pairs are ordered (a < b) and unique within one call. Dismissed rows are
-- left alone, so a dismissal is never undone by the nightly job.
INSERT INTO merge_suggestions (id, problem_a_id, problem_b_id, similarity)
SELECT unnest(sqlc.arg(ids)::uuid[]), unnest(sqlc.arg(a_ids)::uuid[]),
       unnest(sqlc.arg(b_ids)::uuid[]), unnest(sqlc.arg(similarities)::real[])
ON CONFLICT (problem_a_id, problem_b_id) DO UPDATE
  SET similarity = EXCLUDED.similarity
  WHERE merge_suggestions.dismissed_at IS NULL;

-- name: RecentVoteWeightsWithEmbeddings :many
-- Summed vote weight received in the window by each candidate problem (on any
-- of its revisions), with its current embedding for clustering. Nightly only.
SELECT p.id AS problem_id, w.weight, r.embedding::text AS embedding
FROM (
  SELECT pr.problem_id, SUM(v.weight)::float8 AS weight
  FROM problem_revision_votes v
  JOIN problem_revisions pr ON pr.id = v.revision_id
  WHERE v.created_at >= sqlc.arg(since)
  GROUP BY pr.problem_id
) w
JOIN problems p ON p.id = w.problem_id
JOIN problem_revisions r ON r.id = p.current_revision_id
WHERE p.id = ANY(sqlc.arg(ids)::uuid[]) AND p.on_meta_board = false AND w.weight > 0
ORDER BY w.weight DESC, p.id;

-- name: ClearTrending :exec
DELETE FROM trending_problems;

-- name: InsertTrending :exec
INSERT INTO trending_problems (problem_id, cluster_id, rank, computed_at)
SELECT unnest(sqlc.arg(problem_ids)::uuid[]), unnest(sqlc.arg(cluster_ids)::int[]),
       unnest(sqlc.arg(ranks)::int[]), sqlc.arg(computed_at)::timestamptz;

-- name: ListTrending :many
SELECT p.id AS problem_id, p.state, p.soft_solved, p.author_display,
       r.title, d.slug AS domain_slug, d.name AS domain_name,
       u.handle AS author_handle, (u.deleted_at IS NOT NULL)::boolean AS author_deleted,
       COALESCE(s.tier, 'member')::text AS author_tier,
       t.cluster_id, t.rank
FROM trending_problems t
JOIN problems p ON p.id = t.problem_id
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN domains d ON d.id = p.domain_id
JOIN users u ON u.id = p.author_id
LEFT JOIN user_domain_standing s ON s.user_id = p.author_id AND s.domain_id = p.domain_id
WHERE p.state <> 'invalid'
ORDER BY t.rank
LIMIT 50;

-- name: ListMergeSuggestions :many
-- Open suggestions for /admin, most similar first, keyset on (similarity, id).
SELECT m.id, m.similarity, m.created_at,
       m.problem_a_id, ra.title AS title_a,
       m.problem_b_id, rb.title AS title_b
FROM merge_suggestions m
JOIN problems pa ON pa.id = m.problem_a_id
JOIN problem_revisions ra ON ra.id = pa.current_revision_id
JOIN problems pb ON pb.id = m.problem_b_id
JOIN problem_revisions rb ON rb.id = pb.current_revision_id
WHERE m.dismissed_at IS NULL
  AND pa.state <> 'invalid' AND pb.state <> 'invalid'
  AND (m.similarity, m.id) < (sqlc.arg(after_similarity)::real, sqlc.arg(after_id)::uuid)
ORDER BY m.similarity DESC, m.id DESC
LIMIT sqlc.arg(lim);

-- name: DismissMergeSuggestion :execrows
UPDATE merge_suggestions
SET dismissed_at = COALESCE(dismissed_at, now())
WHERE id = $1;
