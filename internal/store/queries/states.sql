-- Problem state changes by the poster. Every change appends a
-- problem_state_events row in the same transaction.

-- name: WriteProblemState :exec
UPDATE problems SET state = sqlc.arg(state), updated_at = sqlc.arg(now) WHERE id = sqlc.arg(id);

-- name: AppendProblemStateEvent :exec
INSERT INTO problem_state_events (id, problem_id, from_state, to_state, actor_id, reason, created_at)
VALUES (sqlc.arg(id), sqlc.arg(problem_id), sqlc.arg(from_state), sqlc.arg(to_state),
        sqlc.narg(actor_id), sqlc.arg(reason), sqlc.arg(created_at));

-- name: ListProblemStateHistory :many
-- actor_kind, never the actor id: the poster may be anonymous.
SELECT e.id, e.from_state, e.to_state, e.reason, e.created_at,
       (CASE WHEN e.actor_id IS NULL THEN 'system'
             WHEN e.actor_id = p.author_id THEN 'poster'
             ELSE 'moderator' END)::text AS actor_kind
FROM problem_state_events e
JOIN problems p ON p.id = e.problem_id
WHERE e.problem_id = $1
ORDER BY e.created_at, e.id;
