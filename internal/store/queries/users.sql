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

-- name: SetUserHandleAndDirectory :exec
-- /welcome: the new member picks a handle and directory opt-in.
UPDATE users SET handle = $2, in_directory = $3
WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdateUserSettings :exec
UPDATE users
SET handle = $2, in_directory = $3, declared_history = $4, email = $5
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListIdentitiesByUser :many
SELECT provider, provider_uid, profile_url, created_at
FROM identities WHERE user_id = $1
ORDER BY created_at, provider;

-- name: ListDirectory :many
-- /members: opted-in, not deleted, keyset by (created_at, id).
SELECT id, handle, display_name, declared_history, created_at
FROM users
WHERE in_directory AND deleted_at IS NULL
  AND (sqlc.narg(after_created_at)::timestamptz IS NULL
       OR (created_at, id) > (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid))
ORDER BY created_at, id
LIMIT sqlc.arg(max_rows);
