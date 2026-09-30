-- name: CreateUser :one
INSERT INTO users (id, handle, display_name)
VALUES ($1, $2, $3)
RETURNING *;

-- name: CreateIdentity :exec
INSERT INTO identities (user_id, provider, provider_uid, profile_url)
VALUES ($1, $2, $3, $4);

-- name: GetIdentity :one
SELECT * FROM identities WHERE provider = $1 AND provider_uid = $2;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByHandle :one
SELECT * FROM users WHERE handle = $1;
