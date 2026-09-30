-- Problem revisions: immutable rows; the current one is chosen by the
-- PickCurrentRevision job from stored scores.

-- name: InsertProblemRevision :exec
INSERT INTO problem_revisions (id, problem_id, parent_revision_id, author_id, author_display,
                               title, current_process, pain, tried, why_note, created_at)
VALUES (sqlc.arg(id), sqlc.arg(problem_id), sqlc.narg(parent_revision_id), sqlc.arg(author_id),
        sqlc.arg(author_display), sqlc.arg(title), sqlc.arg(current_process), sqlc.arg(pain),
        sqlc.arg(tried), sqlc.narg(why_note), sqlc.arg(created_at));

-- name: GetProblemRevisionText :one
SELECT id, problem_id, title, current_process, pain, tried
FROM problem_revisions WHERE id = $1;

-- name: GetReviseSource :one
-- The revision a revise form starts from: the given one, or the current one.
SELECT p.id AS problem_id, p.state, d.id AS domain_id, d.name AS domain_name,
       r.id AS revision_id, r.title, r.current_process, r.pain, r.tried,
       (r.id = p.current_revision_id)::bool AS is_current
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions r ON r.problem_id = p.id
 AND r.id = COALESCE(sqlc.narg(revision_id)::uuid, p.current_revision_id)
WHERE p.id = sqlc.arg(problem_id);

-- name: GetEvolutionHeader :one
SELECT p.id, p.state, p.current_revision_id, d.name AS domain_name, cr.title
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions cr ON cr.id = p.current_revision_id
WHERE p.id = $1;

-- name: ListProblemRevisionsForTree :many
-- One flat query; the tree is assembled in Go by walking parent_revision_id.
SELECT r.id, r.parent_revision_id, r.title, r.current_process, r.pain, r.tried, r.why_note,
       r.score, r.vote_count, r.created_at,
       r.author_display, u.handle AS author_handle, (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       COALESCE(st.tier::text, 'member')::text AS author_tier,
       ((r.author_id = sqlc.narg(viewer_id)::uuid) IS TRUE)::bool AS viewer_is_author,
       EXISTS (SELECT 1 FROM problem_revision_votes v
               WHERE v.revision_id = r.id AND v.user_id = sqlc.narg(viewer_id)::uuid)::bool AS viewer_voted
FROM problem_revisions r
JOIN problems p ON p.id = r.problem_id
JOIN users u ON u.id = r.author_id
LEFT JOIN user_domain_standing st ON st.user_id = r.author_id AND st.domain_id = p.domain_id
WHERE r.problem_id = sqlc.arg(problem_id)
ORDER BY r.created_at, r.id;

-- name: LockProblemForPick :one
SELECT current_revision_id FROM problems WHERE id = $1 FOR UPDATE;

-- name: ListProblemRevisionCandidates :many
-- FOR SHARE: waits for any vote transaction that has already updated one of
-- these rows, so the pick never decides on scores a committed-later vote changed.
SELECT id, score, vote_count FROM problem_revisions
WHERE problem_id = $1
ORDER BY score DESC, id
FOR SHARE;

-- name: SetProblemCurrentRevision :execrows
-- One statement: current revision, the problem's score (= the leader's) and updated_at.
UPDATE problems p
SET current_revision_id = r.id,
    score = r.score,
    updated_at = CASE WHEN p.current_revision_id IS DISTINCT FROM r.id THEN sqlc.arg(now)::timestamptz ELSE p.updated_at END
FROM problem_revisions r
WHERE p.id = sqlc.arg(problem_id) AND r.id = sqlc.arg(revision_id) AND r.problem_id = p.id;
