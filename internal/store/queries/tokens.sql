-- name: CreateToken :one
INSERT INTO automation_token (workspace_id, name, hash, commands) VALUES (?, ?, ?, ?) RETURNING *;


-- name: FindTokenByHash :one
SELECT * FROM automation_token WHERE hash = ? AND revoked_at IS NULL;


-- name: ListTokens :many
SELECT * FROM automation_token WHERE workspace_id = ? ORDER BY id;


-- name: RevokeToken :execrows
UPDATE automation_token SET revoked_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL;
