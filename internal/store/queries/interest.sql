-- Founder interest: "I'd consider building for this" toggle.

-- name: InsertFounderInterest :execrows
INSERT INTO founder_interest (problem_id, user_id, created_at)
VALUES (sqlc.arg(problem_id), sqlc.arg(user_id), sqlc.arg(created_at))
ON CONFLICT DO NOTHING;

-- name: DeleteFounderInterest :execrows
DELETE FROM founder_interest WHERE problem_id = $1 AND user_id = $2;

-- name: CountFounderInterest :one
SELECT count(*)::int8 FROM founder_interest WHERE problem_id = $1;
