-- Reports: "flag this" from any signed-in user (POST /report).

-- name: ReportProblemRevisionExists :one
SELECT EXISTS (SELECT 1 FROM problem_revisions WHERE id = $1);

-- name: ReportSolutionRevisionExists :one
SELECT EXISTS (SELECT 1 FROM solution_revisions WHERE id = $1);

-- name: ReportUserExists :one
SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL);

-- name: CreateReport :exec
INSERT INTO reports (id, reporter_id, target_kind, target_id, reason)
VALUES ($1, $2, $3, $4, $5);
