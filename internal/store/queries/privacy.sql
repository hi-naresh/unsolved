-- name: ExportUserData :one
-- /settings/export: everything the user wrote or did, including anonymous
-- posts, as one JSON document built from a single consistent snapshot. Vote
-- weights and standing points are left out: they reveal the private ranking
-- configuration.
SELECT json_build_object(
  'user', (SELECT row_to_json(x) FROM (
      SELECT id, handle, display_name, in_directory, declared_history, email,
             suspended_at, created_at
      FROM users t WHERE t.id = sqlc.arg(user_id)) x),
  'identities', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT provider, provider_uid, profile_url, created_at
      FROM identities t WHERE t.user_id = sqlc.arg(user_id)) x),
  'problems', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT id, domain_id, author_display, state, current_revision_id,
             forked_from_revision_id, created_at
      FROM problems t WHERE t.author_id = sqlc.arg(user_id)) x),
  'problem_revisions', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT id, problem_id, parent_revision_id, author_display, title,
             current_process, pain, tried, why_note, vote_count, created_at
      FROM problem_revisions t WHERE t.author_id = sqlc.arg(user_id)) x),
  'solutions', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT id, problem_id, author_display, kind, current_revision_id, created_at
      FROM solutions t WHERE t.author_id = sqlc.arg(user_id)) x),
  'solution_revisions', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT id, solution_id, parent_revision_id, author_display, body,
             why_note, vote_count, created_at
      FROM solution_revisions t WHERE t.author_id = sqlc.arg(user_id)) x),
  'problem_revision_votes', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT revision_id, created_at
      FROM problem_revision_votes t WHERE t.user_id = sqlc.arg(user_id)) x),
  'solution_revision_votes', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT revision_id, created_at
      FROM solution_revision_votes t WHERE t.user_id = sqlc.arg(user_id)) x),
  'problem_state_votes', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT problem_id, to_state, created_at
      FROM problem_state_votes t WHERE t.user_id = sqlc.arg(user_id)) x),
  'solution_trials', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT solution_id, outcome, note, created_at
      FROM solution_trials t WHERE t.user_id = sqlc.arg(user_id)) x),
  'founder_interest', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT problem_id, created_at
      FROM founder_interest t WHERE t.user_id = sqlc.arg(user_id)) x),
  'reports_filed', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT id, target_kind, target_id, reason, resolved_at, created_at
      FROM reports t WHERE t.reporter_id = sqlc.arg(user_id)) x),
  'vouches_given', (SELECT coalesce(json_agg(x ORDER BY x.created_at), '[]'::json) FROM (
      SELECT v.domain_id, u.handle AS vouchee_handle, v.created_at
      FROM vouches v JOIN users u ON u.id = v.vouchee_id
      WHERE v.voucher_id = sqlc.arg(user_id)) x)
)::text AS data;

-- name: DeleteUserIdentities :exec
DELETE FROM identities WHERE user_id = $1;

-- name: ScrubUser :execrows
-- Account deletion: the profile is scrubbed, content stays as "deleted user".
UPDATE users
SET handle = sqlc.arg(handle), display_name = 'deleted user', declared_history = NULL,
    email = NULL, in_directory = false, deleted_at = now()
WHERE id = sqlc.arg(id) AND deleted_at IS NULL;
