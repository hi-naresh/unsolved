-- name: ListDomains :many
SELECT * FROM domains ORDER BY id;

-- name: GetDomainBySlug :one
SELECT * FROM domains WHERE slug = $1;
