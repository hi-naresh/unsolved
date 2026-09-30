-- Phase 4 community state vote (problem_state_votes, migration 0005).

-- name: LockProblemForStateVote :one
-- The problem row, locked, with when the poster last acted on it: the latest
-- of its creation, the poster's own state changes and the poster's revisions.
SELECT p.id, p.author_id, p.domain_id, p.state,
       GREATEST(p.created_at,
                (SELECT max(e.created_at) FROM problem_state_events e
                 WHERE e.problem_id = p.id AND e.actor_id = p.author_id),
                (SELECT max(r.created_at) FROM problem_revisions r
                 WHERE r.problem_id = p.id AND r.author_id = p.author_id))::timestamptz AS poster_last_active_at
FROM problems p
WHERE p.id = $1
FOR UPDATE OF p;

-- name: InsertStateVote :execrows
INSERT INTO problem_state_votes (problem_id, user_id, to_state, weight, created_at)
VALUES (sqlc.arg(problem_id), sqlc.arg(user_id), sqlc.arg(to_state), sqlc.arg(weight), sqlc.arg(created_at))
ON CONFLICT DO NOTHING;

-- name: DeleteStateVote :execrows
DELETE FROM problem_state_votes
WHERE problem_id = sqlc.arg(problem_id) AND user_id = sqlc.arg(user_id) AND to_state = sqlc.arg(to_state);

-- name: SumStateVotes :one
SELECT COALESCE(sum(weight), 0)::float8 AS total
FROM problem_state_votes
WHERE problem_id = sqlc.arg(problem_id) AND to_state = sqlc.arg(to_state);

-- name: ClearStateVotes :exec
-- A concluded vote is cleared, so a later reopen starts from zero.
DELETE FROM problem_state_votes
WHERE problem_id = sqlc.arg(problem_id) AND to_state = sqlc.arg(to_state);

-- name: AdminZeroStateVoteWeights :execrows
UPDATE problem_state_votes SET weight = 0
WHERE user_id = $1 AND weight <> 0;
