-- Votes attach to revisions. A vote is one row plus a single-row score
-- update; RescoreAll rebuilds the stored scores from these tables.

-- name: GetProblemRevisionVoteTarget :one
SELECT r.id, r.problem_id, r.author_id, p.domain_id, p.state
FROM problem_revisions r
JOIN problems p ON p.id = r.problem_id
WHERE r.id = $1;

-- name: InsertProblemRevisionVote :one
INSERT INTO problem_revision_votes (revision_id, user_id, weight, created_at)
VALUES (sqlc.arg(revision_id), sqlc.arg(user_id), sqlc.arg(weight), sqlc.arg(created_at))
ON CONFLICT DO NOTHING
RETURNING weight, created_at;

-- name: DeleteProblemRevisionVote :one
DELETE FROM problem_revision_votes
WHERE revision_id = $1 AND user_id = $2
RETURNING weight, created_at;

-- name: AddProblemRevisionScore :one
UPDATE problem_revisions
SET score = score + sqlc.arg(delta)::float8,
    vote_count = vote_count + sqlc.arg(count_delta)::int4
WHERE id = sqlc.arg(id)
RETURNING vote_count;

-- name: GetSolutionRevisionVoteTarget :one
SELECT sr.id, sr.solution_id, sr.author_id, s.problem_id, p.domain_id, p.state
FROM solution_revisions sr
JOIN solutions s ON s.id = sr.solution_id
JOIN problems p ON p.id = s.problem_id
WHERE sr.id = $1;

-- name: InsertSolutionRevisionVote :one
INSERT INTO solution_revision_votes (revision_id, user_id, weight, created_at)
VALUES (sqlc.arg(revision_id), sqlc.arg(user_id), sqlc.arg(weight), sqlc.arg(created_at))
ON CONFLICT DO NOTHING
RETURNING weight, created_at;

-- name: DeleteSolutionRevisionVote :one
DELETE FROM solution_revision_votes
WHERE revision_id = $1 AND user_id = $2
RETURNING weight, created_at;

-- name: AddSolutionRevisionScore :one
UPDATE solution_revisions
SET score = score + sqlc.arg(delta)::float8,
    vote_count = vote_count + sqlc.arg(count_delta)::int4
WHERE id = sqlc.arg(id)
RETURNING vote_count;

-- name: LockProblemRevisionsForRescore :many
-- Keyset batch (by id). FOR UPDATE makes concurrent votes on these rows wait
-- until the batch's rebuilt scores are committed, so no vote is lost.
SELECT r.id, r.problem_id FROM problem_revisions r
WHERE r.id > sqlc.arg(after_id)::uuid
  AND (NOT sqlc.arg(only_listed)::bool OR r.problem_id = ANY(sqlc.arg(problem_ids)::uuid[]))
ORDER BY r.id
LIMIT sqlc.arg(lim)
FOR UPDATE;

-- name: ListProblemRevisionVotesFor :many
SELECT revision_id, weight, created_at FROM problem_revision_votes
WHERE revision_id = ANY(sqlc.arg(revision_ids)::uuid[]);

-- name: SetProblemRevisionScores :exec
UPDATE problem_revisions r
SET score = u.score, vote_count = u.vote_count
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id,
             unnest(sqlc.arg(scores)::float8[]) AS score,
             unnest(sqlc.arg(counts)::int4[]) AS vote_count) u
WHERE r.id = u.id;

-- name: LockSolutionRevisionsForRescore :many
SELECT sr.id, sr.solution_id FROM solution_revisions sr
JOIN solutions s ON s.id = sr.solution_id
WHERE sr.id > sqlc.arg(after_id)::uuid
  AND (NOT sqlc.arg(only_listed)::bool OR s.problem_id = ANY(sqlc.arg(problem_ids)::uuid[]))
ORDER BY sr.id
LIMIT sqlc.arg(lim)
FOR UPDATE OF sr;

-- name: ListSolutionRevisionVotesFor :many
SELECT revision_id, weight, created_at FROM solution_revision_votes
WHERE revision_id = ANY(sqlc.arg(revision_ids)::uuid[]);

-- name: SetSolutionRevisionScores :exec
UPDATE solution_revisions r
SET score = u.score, vote_count = u.vote_count
FROM (SELECT unnest(sqlc.arg(ids)::uuid[]) AS id,
             unnest(sqlc.arg(scores)::float8[]) AS score,
             unnest(sqlc.arg(counts)::int4[]) AS vote_count) u
WHERE r.id = u.id;
