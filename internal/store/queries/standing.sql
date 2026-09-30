-- Phase 4 reputation: user_domain_standing is computed by the
-- RecomputeStanding job from verifiable signals only (never from declared
-- history), and read on every vote by the tier VoteWeigher.

-- name: GetVoterStanding :one
-- The tier weigher's one indexed read per vote: users PK + standing PK.
SELECT u.created_at, (u.suspended_at IS NOT NULL)::bool AS suspended,
       COALESCE(st.tier::text, 'member')::text AS tier
FROM users u
LEFT JOIN user_domain_standing st ON st.user_id = u.id AND st.domain_id = sqlc.arg(domain_id)::smallint
WHERE u.id = sqlc.arg(user_id)::uuid;

-- name: GetStandingTier :one
SELECT COALESCE((SELECT st.tier::text FROM user_domain_standing st
                 WHERE st.user_id = sqlc.arg(user_id)::uuid AND st.domain_id = sqlc.arg(domain_id)::smallint),
                'member')::text AS tier;

-- name: ListStandingSignals :many
-- Per (user, domain, kind) counts of every signal standing points come from,
-- for a batch of users. Only stored counters are read (vote_count), never
-- vote rows. Invalid problems earn nothing. Anonymous content counts.
-- kind: solved | current_first | current_later | top_solution | votes |
-- vouches | existing (a standing row that may need resetting).
-- domain_id = 0 means every domain.
SELECT sig.user_id::uuid AS user_id, sig.domain_id::smallint AS domain_id,
       sig.kind::text AS kind, sig.n::float8 AS n
FROM (
  SELECT p.author_id AS user_id, p.domain_id, 'solved' AS kind, count(*)::float8 AS n
  FROM problems p
  WHERE p.author_id = ANY(sqlc.arg(user_ids)::uuid[]) AND p.state = 'solved'
  GROUP BY p.author_id, p.domain_id
  UNION ALL
  -- current problem revisions; a first revision counts only once someone
  -- else has voted for it
  SELECT r.author_id, p.domain_id,
         CASE WHEN r.parent_revision_id IS NULL THEN 'current_first' ELSE 'current_later' END,
         count(*)::float8
  FROM problems p
  JOIN problem_revisions r ON r.id = p.current_revision_id
  WHERE r.author_id = ANY(sqlc.arg(user_ids)::uuid[]) AND p.state <> 'invalid'
    AND (r.parent_revision_id IS NOT NULL OR r.vote_count >= 1)
  GROUP BY r.author_id, p.domain_id, 3
  UNION ALL
  -- current solution revisions, same rule
  SELECT r.author_id, p.domain_id,
         CASE WHEN r.parent_revision_id IS NULL THEN 'current_first' ELSE 'current_later' END,
         count(*)::float8
  FROM solutions s
  JOIN solution_revisions r ON r.id = s.current_revision_id
  JOIN problems p ON p.id = s.problem_id
  WHERE r.author_id = ANY(sqlc.arg(user_ids)::uuid[]) AND p.state <> 'invalid'
    AND (r.parent_revision_id IS NOT NULL OR r.vote_count >= 1)
  GROUP BY r.author_id, p.domain_id, 3
  UNION ALL
  -- the top solution (by stored score) of a soft-solved or solved problem
  SELECT t.author_id, p.domain_id, 'top_solution', count(*)::float8
  FROM problems p
  CROSS JOIN LATERAL (
    SELECT s.author_id FROM solutions s
    WHERE s.problem_id = p.id
    ORDER BY s.score DESC, s.id
    LIMIT 1
  ) t
  WHERE (p.soft_solved OR p.state = 'solved') AND p.state <> 'invalid'
    AND t.author_id = ANY(sqlc.arg(user_ids)::uuid[])
  GROUP BY t.author_id, p.domain_id
  UNION ALL
  -- net votes received (stored vote_count) on problem and solution revisions
  SELECT v.author_id, v.domain_id, 'votes', sum(v.vote_count)::float8
  FROM (
    SELECT r.author_id, p.domain_id, r.vote_count
    FROM problem_revisions r
    JOIN problems p ON p.id = r.problem_id
    WHERE r.author_id = ANY(sqlc.arg(user_ids)::uuid[]) AND p.state <> 'invalid'
    UNION ALL
    SELECT r.author_id, p.domain_id, r.vote_count
    FROM solution_revisions r
    JOIN solutions s ON s.id = r.solution_id
    JOIN problems p ON p.id = s.problem_id
    WHERE r.author_id = ANY(sqlc.arg(user_ids)::uuid[]) AND p.state <> 'invalid'
  ) v
  GROUP BY v.author_id, v.domain_id
  UNION ALL
  -- vouches from members who are contributors or experts in that domain
  SELECT v.vouchee_id, v.domain_id, 'vouches', count(*)::float8
  FROM vouches v
  JOIN user_domain_standing vs ON vs.user_id = v.voucher_id AND vs.domain_id = v.domain_id
  WHERE v.vouchee_id = ANY(sqlc.arg(user_ids)::uuid[])
    AND vs.tier IN ('domain_contributor', 'domain_expert')
  GROUP BY v.vouchee_id, v.domain_id
  UNION ALL
  SELECT st.user_id, st.domain_id, 'existing', 0::float8
  FROM user_domain_standing st
  WHERE st.user_id = ANY(sqlc.arg(user_ids)::uuid[])
) sig
WHERE sqlc.arg(domain_id)::smallint = 0 OR sig.domain_id = sqlc.arg(domain_id)::smallint;

-- name: ListDeclaredHistoryUsers :many
-- Which of these users have a non-empty declared history.
SELECT id FROM users
WHERE id = ANY(sqlc.arg(user_ids)::uuid[])
  AND declared_history IS NOT NULL AND btrim(declared_history) <> '';

-- name: ListStandingUserBatch :many
-- Nightly full pass: users with any activity, keyset by id.
SELECT u.id FROM users u
WHERE u.id > sqlc.arg(after_id)::uuid
  AND (EXISTS (SELECT 1 FROM problems p WHERE p.author_id = u.id)
       OR EXISTS (SELECT 1 FROM problem_revisions r WHERE r.author_id = u.id)
       OR EXISTS (SELECT 1 FROM solution_revisions r WHERE r.author_id = u.id)
       OR EXISTS (SELECT 1 FROM solutions s WHERE s.author_id = u.id)
       OR EXISTS (SELECT 1 FROM vouches v WHERE v.vouchee_id = u.id)
       OR EXISTS (SELECT 1 FROM user_domain_standing st WHERE st.user_id = u.id))
ORDER BY u.id
LIMIT sqlc.arg(lim);

-- name: UpsertStandings :exec
-- Writes only rows whose tier or points changed, so reruns are no-ops.
INSERT INTO user_domain_standing (user_id, domain_id, tier, points, updated_at)
SELECT unnest(sqlc.arg(user_ids)::uuid[]), unnest(sqlc.arg(domain_ids)::smallint[]),
       unnest(sqlc.arg(tiers)::text[])::standing_tier, unnest(sqlc.arg(points)::float8[]),
       sqlc.arg(now)::timestamptz
ON CONFLICT (user_id, domain_id) DO UPDATE
  SET tier = EXCLUDED.tier, points = EXCLUDED.points, updated_at = EXCLUDED.updated_at
  WHERE (user_domain_standing.tier, user_domain_standing.points) IS DISTINCT FROM (EXCLUDED.tier, EXCLUDED.points);

-- name: GetRevisionAuthorDomain :one
-- Author and domain of a problem revision (standing triggers).
SELECT r.author_id, p.domain_id
FROM problem_revisions r
JOIN problems p ON p.id = r.problem_id
WHERE r.id = $1;

-- name: GetSolutionRevisionAuthorDomain :one
SELECT r.author_id, p.domain_id
FROM solution_revisions r
JOIN solutions s ON s.id = r.solution_id
JOIN problems p ON p.id = s.problem_id
WHERE r.id = $1;

-- name: GetTopSolutionAuthor :one
-- Author of the problem's top solution (standing triggers on state changes).
SELECT s.author_id, p.domain_id
FROM solutions s
JOIN problems p ON p.id = s.problem_id
WHERE s.problem_id = $1
ORDER BY s.score DESC, s.id
LIMIT 1;
