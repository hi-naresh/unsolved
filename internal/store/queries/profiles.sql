-- name: ListNamedContributions :many
-- A user's public profile: named problem and solution revisions only.
-- Anonymous rows are filtered here and must never reach the profile page.
SELECT c.kind, c.problem_id, c.title, c.is_first, c.created_at
FROM (
  SELECT 'problem'::text AS kind, pr.problem_id, pr.title,
         (pr.parent_revision_id IS NULL)::boolean AS is_first, pr.created_at, pr.id
  FROM problem_revisions pr
  WHERE pr.author_id = sqlc.arg(user_id) AND pr.author_display = 'named'
  UNION ALL
  SELECT 'solution'::text, s.problem_id, cur.title,
         (sr.parent_revision_id IS NULL)::boolean, sr.created_at, sr.id
  FROM solution_revisions sr
  JOIN solutions s ON s.id = sr.solution_id
  JOIN problems p ON p.id = s.problem_id
  JOIN problem_revisions cur ON cur.id = p.current_revision_id
  WHERE sr.author_id = sqlc.arg(user_id) AND sr.author_display = 'named'
) c
ORDER BY c.created_at DESC, c.id DESC
LIMIT sqlc.arg(max_rows);
