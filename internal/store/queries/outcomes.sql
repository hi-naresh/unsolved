-- Phase 3: the SolvedPrompt email and its one-tap outcome link.

-- name: GetSolvedPromptContext :one
-- Everything the SolvedPrompt email and the /solved/{token} confirm page
-- show: the problem (current title), its poster, and the solution's current
-- revision. poster_outcome is the poster's existing "tried it" outcome, or empty.
SELECT p.id AS problem_id, p.author_id, p.state, p.soft_solved,
       pr.title,
       s.id AS solution_id, s.kind, sr.body,
       u.email,
       (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       (u.suspended_at IS NOT NULL)::bool AS author_suspended,
       COALESCE((SELECT t.outcome::text FROM solution_trials t
                 WHERE t.solution_id = s.id AND t.user_id = p.author_id), '')::text AS poster_outcome
FROM problems p
JOIN problem_revisions pr ON pr.id = p.current_revision_id
JOIN solutions s ON s.id = sqlc.arg(solution_id) AND s.problem_id = p.id
JOIN solution_revisions sr ON sr.id = s.current_revision_id
JOIN users u ON u.id = p.author_id
WHERE p.id = sqlc.arg(problem_id);

-- name: LockOutcomeTarget :one
-- Locks the problem row for the one-tap outcome write and checks the solution
-- belongs to it (no row otherwise).
SELECT p.id, p.author_id, p.state,
       (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       (u.suspended_at IS NOT NULL)::bool AS author_suspended
FROM problems p
JOIN solutions s ON s.problem_id = p.id AND s.id = sqlc.arg(solution_id)
JOIN users u ON u.id = p.author_id
WHERE p.id = sqlc.arg(problem_id)
FOR UPDATE OF p;
