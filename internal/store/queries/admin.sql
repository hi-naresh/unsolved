-- /admin moderation. Admins are ADMIN_HANDLES; there is no admin role in the DB.

-- name: AdminListOpenReports :many
-- Unresolved reports, oldest first, keyset on (created_at, id). For
-- revisions the author's handle is returned only when the revision is named;
-- author ids are never returned.
SELECT
  r.id,
  r.target_kind,
  r.target_id,
  r.reason,
  r.created_at,
  rep.handle AS reporter_handle,
  COALESCE(pr.title, '')::text AS target_title,
  COALESCE(left(pr.current_process, 280), left(sr.body, 280), '')::text AS target_excerpt,
  COALESCE(pr.problem_id::text, s.problem_id::text, '')::text AS target_problem_id,
  COALESCE(COALESCE(pr.author_display, sr.author_display) = 'anonymous', false)::boolean AS target_anonymous,
  (CASE
     WHEN r.target_kind = 'user' OR COALESCE(pr.author_display, sr.author_display) = 'named' THEN COALESCE(tu.handle, '')
     ELSE ''
   END)::text AS target_handle,
  (tu.suspended_at IS NOT NULL)::boolean AS target_suspended,
  (tu.deleted_at IS NOT NULL)::boolean AS target_deleted,
  COALESCE(p.state::text, '')::text AS problem_state,
  COALESCE(p.on_meta_board, false)::boolean AS problem_on_meta,
  -- the revision author's tier in the problem's domain (shown with Anonymous)
  COALESCE(ts.tier::text, 'member')::text AS target_tier
FROM reports r
JOIN users rep ON rep.id = r.reporter_id
LEFT JOIN problem_revisions pr ON r.target_kind = 'problem_revision' AND pr.id = r.target_id
LEFT JOIN solution_revisions sr ON r.target_kind = 'solution_revision' AND sr.id = r.target_id
LEFT JOIN solutions s ON s.id = sr.solution_id
LEFT JOIN problems p ON p.id = COALESCE(pr.problem_id, s.problem_id)
LEFT JOIN users tu ON tu.id = CASE WHEN r.target_kind = 'user' THEN r.target_id ELSE COALESCE(pr.author_id, sr.author_id) END
LEFT JOIN user_domain_standing ts ON r.target_kind <> 'user' AND ts.user_id = tu.id AND ts.domain_id = p.domain_id
WHERE r.resolved_at IS NULL
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (r.created_at, r.id) > (sqlc.narg(after_created)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY r.created_at, r.id
LIMIT sqlc.arg(lim);

-- name: AdminResolveReport :execrows
UPDATE reports SET resolved_at = now() WHERE id = $1 AND resolved_at IS NULL;

-- name: AdminSetProblemState :exec
UPDATE problems SET state = $2, updated_at = now() WHERE id = $1;

-- name: AdminInsertStateEvent :exec
INSERT INTO problem_state_events (id, problem_id, from_state, to_state, actor_id, reason)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: AdminToggleMetaBoard :one
UPDATE problems SET on_meta_board = NOT on_meta_board, updated_at = now()
WHERE id = $1
RETURNING on_meta_board;

-- name: AdminSuspendUser :execrows
UPDATE users SET suspended_at = COALESCE(suspended_at, now()) WHERE id = $1;

-- name: AdminUnsuspendUser :execrows
UPDATE users SET suspended_at = NULL WHERE id = $1;

-- name: AdminZeroProblemVoteWeights :many
-- Zeroes every non-zero problem-revision vote by the user; returns the
-- problem of each affected revision (one row per vote).
UPDATE problem_revision_votes v SET weight = 0
FROM problem_revisions pr
WHERE v.revision_id = pr.id AND v.user_id = $1 AND v.weight <> 0
RETURNING pr.problem_id;

-- name: AdminZeroSolutionVoteWeights :many
-- Same for solution-revision votes; returns the problem each solution is on.
UPDATE solution_revision_votes v SET weight = 0
FROM solution_revisions sr, solutions s
WHERE v.revision_id = sr.id AND s.id = sr.solution_id AND v.user_id = $1 AND v.weight <> 0
RETURNING s.problem_id;
