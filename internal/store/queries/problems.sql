-- Problems: creation, the problem page and the keyset-paginated lists.
-- Every list reads stored scores; nothing is computed from vote rows here.

-- name: InsertProblem :exec
-- current_revision_id is set here; its FK is deferred until the first
-- revision is inserted later in the same transaction.
INSERT INTO problems (id, domain_id, author_id, author_display, current_revision_id,
                      forked_from_revision_id, is_seed, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(domain_id), sqlc.arg(author_id), sqlc.arg(author_display),
        sqlc.arg(current_revision_id), sqlc.narg(forked_from_revision_id), sqlc.arg(is_seed),
        sqlc.arg(now), sqlc.arg(now));

-- name: GetProblemForWrite :one
SELECT id, domain_id, author_id, state, current_revision_id
FROM problems WHERE id = $1;

-- name: GetProblemPage :one
SELECT p.id, p.state, p.soft_solved, p.on_meta_board, p.is_seed, p.created_at,
       d.slug AS domain_slug, d.name AS domain_name,
       r.id AS revision_id, r.title, r.current_process, r.pain, r.tried,
       r.vote_count, r.score, r.created_at AS revision_created_at,
       (r.parent_revision_id IS NOT NULL)::bool AS is_revised,
       r.author_display AS revision_author_display, ru.handle AS revision_author_handle,
       (ru.deleted_at IS NOT NULL)::bool AS revision_author_deleted,
       COALESCE(rs.tier::text, 'member')::text AS revision_author_tier,
       p.author_display AS poster_display, pu.handle AS poster_handle,
       (pu.deleted_at IS NOT NULL)::bool AS poster_deleted,
       COALESCE(ps.tier::text, 'member')::text AS poster_tier,
       ((p.author_id = sqlc.narg(viewer_id)::uuid) IS TRUE)::bool AS viewer_is_poster,
       ((r.author_id = sqlc.narg(viewer_id)::uuid) IS TRUE)::bool AS viewer_is_revision_author,
       EXISTS (SELECT 1 FROM problem_revision_votes v
               WHERE v.revision_id = r.id AND v.user_id = sqlc.narg(viewer_id)::uuid)::bool AS viewer_voted,
       (SELECT count(*) FROM founder_interest fi WHERE fi.problem_id = p.id)::int8 AS interest_count,
       EXISTS (SELECT 1 FROM founder_interest fi
               WHERE fi.problem_id = p.id AND fi.user_id = sqlc.narg(viewer_id)::uuid)::bool AS viewer_interested,
       fr.problem_id AS forked_from_problem_id, fr.title AS forked_from_title,
       COALESCE((SELECT jsonb_agg(jsonb_build_object('id', f.id, 'title', fcr.title) ORDER BY f.created_at)
                 FROM problems f
                 JOIN problem_revisions fcr ON fcr.id = f.current_revision_id
                 WHERE f.forked_from_revision_id IN
                       (SELECT pr.id FROM problem_revisions pr WHERE pr.problem_id = p.id)),
                '[]'::jsonb)::jsonb AS forks,
       -- phase 4 community state vote: summed weights, the viewer's votes and
       -- tier, and when the poster last acted (the 14-day silence rule)
       COALESCE((SELECT sum(sv.weight) FROM problem_state_votes sv
                 WHERE sv.problem_id = p.id AND sv.to_state = 'solved'), 0)::float8 AS solved_vote_weight,
       COALESCE((SELECT sum(sv.weight) FROM problem_state_votes sv
                 WHERE sv.problem_id = p.id AND sv.to_state = 'invalid'), 0)::float8 AS invalid_vote_weight,
       EXISTS (SELECT 1 FROM problem_state_votes sv WHERE sv.problem_id = p.id AND sv.to_state = 'solved'
                 AND sv.user_id = sqlc.narg(viewer_id)::uuid)::bool AS viewer_voted_solved,
       EXISTS (SELECT 1 FROM problem_state_votes sv WHERE sv.problem_id = p.id AND sv.to_state = 'invalid'
                 AND sv.user_id = sqlc.narg(viewer_id)::uuid)::bool AS viewer_voted_invalid,
       COALESCE(vs.tier::text, 'member')::text AS viewer_tier,
       GREATEST(p.created_at,
                (SELECT max(e.created_at) FROM problem_state_events e
                 WHERE e.problem_id = p.id AND e.actor_id = p.author_id),
                (SELECT max(pr.created_at) FROM problem_revisions pr
                 WHERE pr.problem_id = p.id AND pr.author_id = p.author_id))::timestamptz AS poster_last_active_at
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN users ru ON ru.id = r.author_id
JOIN users pu ON pu.id = p.author_id
LEFT JOIN user_domain_standing rs ON rs.user_id = r.author_id AND rs.domain_id = p.domain_id
LEFT JOIN user_domain_standing ps ON ps.user_id = p.author_id AND ps.domain_id = p.domain_id
LEFT JOIN user_domain_standing vs ON vs.user_id = sqlc.narg(viewer_id)::uuid AND vs.domain_id = p.domain_id
LEFT JOIN problem_revisions fr ON fr.id = p.forked_from_revision_id
WHERE p.id = sqlc.arg(id);

-- name: CountNonSeedProblemsUpTo50 :one
-- Bounded: stops counting at 50, which is all the pinning rule needs.
SELECT count(*)::int8 FROM (SELECT 1 FROM problems WHERE NOT is_seed LIMIT 50) s;

-- name: ListPinnedSeedProblems :many
SELECT p.id, p.score, p.created_at, p.state, p.soft_solved, p.is_seed,
       d.slug AS domain_slug, d.name AS domain_name, r.title, r.vote_count,
       p.author_display, u.handle AS author_handle, (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       COALESCE(st.tier::text, 'member')::text AS author_tier,
       left(r.pain, 220)::text AS pain_excerpt,
       (SELECT count(*) FROM solutions so WHERE so.problem_id = p.id)::int4 AS solution_count
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN users u ON u.id = p.author_id
LEFT JOIN user_domain_standing st ON st.user_id = p.author_id AND st.domain_id = p.domain_id
WHERE p.is_seed AND p.state = 'open' AND NOT p.soft_solved AND NOT p.on_meta_board
ORDER BY p.score DESC, p.id DESC
LIMIT 50;

-- name: ListFrontPage :many
SELECT p.id, p.score, p.created_at, p.state, p.soft_solved, p.is_seed,
       d.slug AS domain_slug, d.name AS domain_name, r.title, r.vote_count,
       p.author_display, u.handle AS author_handle, (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       COALESCE(st.tier::text, 'member')::text AS author_tier,
       left(r.pain, 220)::text AS pain_excerpt,
       (SELECT count(*) FROM solutions so WHERE so.problem_id = p.id)::int4 AS solution_count
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN users u ON u.id = p.author_id
LEFT JOIN user_domain_standing st ON st.user_id = p.author_id AND st.domain_id = p.domain_id
WHERE p.state = 'open' AND NOT p.soft_solved AND NOT p.on_meta_board
  AND (NOT sqlc.arg(exclude_seeds)::bool OR NOT p.is_seed)
  AND (NOT sqlc.arg(has_after)::bool OR (p.score, p.id) < (sqlc.arg(after_score)::float8, sqlc.arg(after_id)::uuid))
ORDER BY p.score DESC, p.id DESC
LIMIT sqlc.arg(lim);

-- name: ListProblemsTop :many
SELECT p.id, p.score, p.created_at, p.state, p.soft_solved, p.is_seed,
       d.slug AS domain_slug, d.name AS domain_name, r.title, r.vote_count,
       p.author_display, u.handle AS author_handle, (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       COALESCE(st.tier::text, 'member')::text AS author_tier,
       left(r.pain, 220)::text AS pain_excerpt,
       (SELECT count(*) FROM solutions so WHERE so.problem_id = p.id)::int4 AS solution_count
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN users u ON u.id = p.author_id
LEFT JOIN user_domain_standing st ON st.user_id = p.author_id AND st.domain_id = p.domain_id
WHERE NOT p.on_meta_board
  AND (sqlc.narg(state)::problem_state IS NULL OR p.state = sqlc.narg(state)::problem_state)
  AND (sqlc.narg(domain_slug)::text IS NULL OR d.slug = sqlc.narg(domain_slug)::text)
  AND (NOT sqlc.arg(has_after)::bool OR (p.score, p.id) < (sqlc.arg(after_score)::float8, sqlc.arg(after_id)::uuid))
ORDER BY p.score DESC, p.id DESC
LIMIT sqlc.arg(lim);

-- name: ListProblemsNew :many
SELECT p.id, p.score, p.created_at, p.state, p.soft_solved, p.is_seed,
       d.slug AS domain_slug, d.name AS domain_name, r.title, r.vote_count,
       p.author_display, u.handle AS author_handle, (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       COALESCE(st.tier::text, 'member')::text AS author_tier,
       left(r.pain, 220)::text AS pain_excerpt,
       (SELECT count(*) FROM solutions so WHERE so.problem_id = p.id)::int4 AS solution_count
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN users u ON u.id = p.author_id
LEFT JOIN user_domain_standing st ON st.user_id = p.author_id AND st.domain_id = p.domain_id
WHERE NOT p.on_meta_board
  AND (sqlc.narg(state)::problem_state IS NULL OR p.state = sqlc.narg(state)::problem_state)
  AND (sqlc.narg(domain_slug)::text IS NULL OR d.slug = sqlc.narg(domain_slug)::text)
  AND (NOT sqlc.arg(has_after)::bool OR (p.created_at, p.id) < (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY p.created_at DESC, p.id DESC
LIMIT sqlc.arg(lim);

-- name: ListMetaBoard :many
SELECT p.id, p.score, p.created_at, p.state, p.soft_solved, p.is_seed,
       d.slug AS domain_slug, d.name AS domain_name, r.title, r.vote_count,
       p.author_display, u.handle AS author_handle, (u.deleted_at IS NOT NULL)::bool AS author_deleted,
       COALESCE(st.tier::text, 'member')::text AS author_tier,
       left(r.pain, 220)::text AS pain_excerpt,
       (SELECT count(*) FROM solutions so WHERE so.problem_id = p.id)::int4 AS solution_count
FROM problems p
JOIN domains d ON d.id = p.domain_id
JOIN problem_revisions r ON r.id = p.current_revision_id
JOIN users u ON u.id = p.author_id
LEFT JOIN user_domain_standing st ON st.user_id = p.author_id AND st.domain_id = p.domain_id
WHERE p.on_meta_board
  AND (NOT sqlc.arg(has_after)::bool OR (p.score, p.id) < (sqlc.arg(after_score)::float8, sqlc.arg(after_id)::uuid))
ORDER BY p.score DESC, p.id DESC
LIMIT sqlc.arg(lim);
