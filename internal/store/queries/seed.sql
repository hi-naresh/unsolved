-- cmd/seed: the five launch seed problems (is_seed = true).

-- name: SeedUpsertSystemUser :one
-- The system author. Idempotent: returns the existing row's id.
INSERT INTO users (id, handle, display_name)
VALUES ($1, $2, $3)
ON CONFLICT (handle) DO UPDATE SET handle = EXCLUDED.handle
RETURNING id;

-- name: SeedProblemExists :one
SELECT EXISTS (
  SELECT 1 FROM problems p
  JOIN problem_revisions r ON r.problem_id = p.id
  WHERE p.author_id = $1 AND p.is_seed AND r.title = $2
);

-- name: SeedCreateProblem :exec
INSERT INTO problems (id, domain_id, author_id, author_display, is_seed)
VALUES ($1, $2, $3, 'named', true);

-- name: SeedCreateRevision :exec
INSERT INTO problem_revisions (id, problem_id, author_id, author_display, title, current_process, pain, tried)
VALUES ($1, $2, $3, 'named', $4, $5, $6, $7);

-- name: SeedSetCurrentRevision :exec
UPDATE problems SET current_revision_id = $2, updated_at = now() WHERE id = $1;
