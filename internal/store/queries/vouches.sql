-- Phase 4 vouching: a domain contributor or expert vouches for another
-- member in that domain. Toggle: insert, or delete if it exists.

-- name: InsertVouch :execrows
INSERT INTO vouches (voucher_id, vouchee_id, domain_id, created_at)
VALUES (sqlc.arg(voucher_id), sqlc.arg(vouchee_id), sqlc.arg(domain_id), sqlc.arg(created_at))
ON CONFLICT DO NOTHING;

-- name: DeleteVouch :execrows
DELETE FROM vouches
WHERE voucher_id = sqlc.arg(voucher_id) AND vouchee_id = sqlc.arg(vouchee_id) AND domain_id = sqlc.arg(domain_id);

-- name: GetProfileReputation :one
-- Everything reputation-related a profile shows, in one row: social links
-- (only when include_links), per-domain tier, vouch counts and the viewer's
-- eligibility to vouch, and whether the user has reached contributor anywhere.
SELECT
  COALESCE((SELECT jsonb_agg(jsonb_build_object('provider', i.provider, 'url', i.profile_url)
                             ORDER BY i.created_at, i.provider)
            FROM identities i
            WHERE i.user_id = sqlc.arg(user_id)::uuid AND sqlc.arg(include_links)::bool
              AND i.profile_url IS NOT NULL),
           '[]'::jsonb)::jsonb AS links,
  COALESCE((SELECT jsonb_agg(jsonb_build_object(
                     'id', d.id, 'slug', d.slug, 'name', d.name,
                     'tier', COALESCE(st.tier::text, 'member'),
                     'vouches', COALESCE(vc.n, 0),
                     'viewer_tier', COALESCE(vt.tier::text, 'member'),
                     'viewer_vouched', (vv.voucher_id IS NOT NULL)) ORDER BY d.id)
            FROM domains d
            LEFT JOIN user_domain_standing st ON st.user_id = sqlc.arg(user_id)::uuid AND st.domain_id = d.id
            LEFT JOIN user_domain_standing vt ON vt.user_id = sqlc.narg(viewer_id)::uuid AND vt.domain_id = d.id
            LEFT JOIN (SELECT v.domain_id, count(*) AS n FROM vouches v
                       WHERE v.vouchee_id = sqlc.arg(user_id)::uuid GROUP BY v.domain_id) vc ON vc.domain_id = d.id
            LEFT JOIN vouches vv ON vv.voucher_id = sqlc.narg(viewer_id)::uuid
                                AND vv.vouchee_id = sqlc.arg(user_id)::uuid AND vv.domain_id = d.id),
           '[]'::jsonb)::jsonb AS domains,
  EXISTS (SELECT 1 FROM user_domain_standing s
          WHERE s.user_id = sqlc.arg(user_id)::uuid
            AND s.tier IN ('domain_contributor', 'domain_expert'))::bool AS reached_contributor;
