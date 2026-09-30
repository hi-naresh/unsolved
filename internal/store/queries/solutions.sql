-- Solutions: same shape as problems (identity + immutable revisions), plus
-- "tried it" outcomes.

-- name: InsertSolution :exec
INSERT INTO solutions (id, problem_id, author_id, author_display, kind, current_revision_id, created_at)
VALUES (sqlc.arg(id), sqlc.arg(problem_id), sqlc.arg(author_id), sqlc.arg(author_display),
        sqlc.arg(kind), sqlc.arg(current_revision_id), sqlc.arg(created_at));

-- name: InsertSolutionRevision :exec
INSERT INTO solution_revisions (id, solution_id, parent_revision_id, author_id, author_display,
                                body, why_note, created_at)
VALUES (sqlc.arg(id), sqlc.arg(solution_id), sqlc.narg(parent_revision_id), sqlc.arg(author_id),
        sqlc.arg(author_display), sqlc.arg(body), sqlc.narg(why_note), sqlc.arg(created_at));

-- name: GetSolutionForWrite :one
SELECT s.id, s.problem_id, s.current_revision_id, p.state, p.domain_id
FROM solutions s
JOIN problems p ON p.id = s.problem_id
WHERE s.id = $1;

-- name: GetSolutionRevisionOwner :one
SELECT id, solution_id FROM solution_revisions WHERE id = $1;

-- name: ListProblemSolutions :many
SELECT s.id, s.kind, s.score, s.created_at,
       r.id AS revision_id, r.body, r.vote_count, r.created_at AS revision_created_at,
       (r.parent_revision_id IS NOT NULL)::bool AS is_revised,
       r.author_display, u.handle AS author_handle, (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       COALESCE(st.tier::text, 'member')::text AS author_tier,
       ((r.author_id = sqlc.narg(viewer_id)::uuid) IS TRUE)::bool AS viewer_is_author,
       EXISTS (SELECT 1 FROM solution_revision_votes v
               WHERE v.revision_id = r.id AND v.user_id = sqlc.narg(viewer_id)::uuid)::bool AS viewer_voted,
       t.worked::int8 AS worked, t.partly::int8 AS partly, t.failed::int8 AS failed,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('outcome', n.outcome, 'note', n.note) ORDER BY n.created_at DESC)
                 FROM (SELECT tt.outcome, tt.note, tt.created_at FROM solution_trials tt
                       WHERE tt.solution_id = s.id AND tt.note IS NOT NULL AND tt.note <> ''
                       ORDER BY tt.created_at DESC LIMIT 3) n),
                '[]'::jsonb)::jsonb AS recent_notes,
       COALESCE((SELECT vt.outcome::text FROM solution_trials vt
                 WHERE vt.solution_id = s.id AND vt.user_id = sqlc.narg(viewer_id)::uuid), '')::text AS viewer_outcome
FROM solutions s
JOIN problems p ON p.id = s.problem_id
JOIN solution_revisions r ON r.id = s.current_revision_id
JOIN users u ON u.id = r.author_id
LEFT JOIN user_domain_standing st ON st.user_id = r.author_id AND st.domain_id = p.domain_id
CROSS JOIN LATERAL (
  SELECT count(*) FILTER (WHERE outcome = 'worked') AS worked,
         count(*) FILTER (WHERE outcome = 'partly') AS partly,
         count(*) FILTER (WHERE outcome = 'failed') AS failed
  FROM solution_trials WHERE solution_id = s.id
) t
WHERE s.problem_id = sqlc.arg(problem_id)
ORDER BY s.score DESC, s.id
LIMIT 100;

-- name: LockSolutionForPick :one
SELECT current_revision_id, problem_id FROM solutions WHERE id = $1 FOR UPDATE;

-- name: ListSolutionRevisionCandidates :many
SELECT id, score, vote_count FROM solution_revisions
WHERE solution_id = $1
ORDER BY score DESC, id
FOR SHARE;

-- name: SetSolutionCurrentRevision :execrows
-- One statement: current revision and the solution's score (= the leader's).
UPDATE solutions s
SET current_revision_id = r.id, score = r.score
FROM solution_revisions r
WHERE s.id = sqlc.arg(solution_id) AND r.id = sqlc.arg(revision_id) AND r.solution_id = s.id;

-- name: LockProblemSoftSolved :one
SELECT soft_solved FROM problems WHERE id = $1 FOR UPDATE;

-- name: GetTopSolution :one
-- The problem's top solution by stored score, with its current revision's raw vote count.
SELECT s.id, r.vote_count
FROM solutions s
JOIN solution_revisions r ON r.id = s.current_revision_id
WHERE s.problem_id = $1
ORDER BY s.score DESC, s.id
LIMIT 1;

-- name: SetProblemSoftSolved :exec
UPDATE problems SET soft_solved = sqlc.arg(soft_solved) WHERE id = sqlc.arg(id);

-- name: UpsertSolutionTrial :exec
INSERT INTO solution_trials (solution_id, user_id, outcome, note, created_at)
VALUES (sqlc.arg(solution_id), sqlc.arg(user_id), sqlc.arg(outcome), sqlc.narg(note), sqlc.arg(created_at))
ON CONFLICT (solution_id, user_id)
DO UPDATE SET outcome = EXCLUDED.outcome, note = EXCLUDED.note, created_at = EXCLUDED.created_at;
