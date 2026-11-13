-- name: CreateToken :one
INSERT INTO automation_token (workspace_id, name, hash, commands, rights) VALUES (?, ?, ?, ?, ?) RETURNING *;


-- name: FindTokenByHash :one
SELECT * FROM automation_token WHERE hash = ? AND revoked_at IS NULL;


-- name: ListTokens :many
SELECT * FROM automation_token WHERE workspace_id = ? ORDER BY id;


-- name: RevokeToken :execrows
UPDATE automation_token SET revoked_at = CAST(sqlc.arg(now) AS TEXT)
WHERE id = sqlc.arg(id) AND workspace_id = sqlc.arg(workspace_id) AND revoked_at IS NULL;


-- name: GetToken :one
SELECT * FROM automation_token WHERE id = ?;


-- name: SetTokenTrusted :execrows
-- The owner vouches for what an automation token writes, or takes it back.
UPDATE automation_token SET trusted = sqlc.arg(trusted)
WHERE id = sqlc.arg(id) AND workspace_id = sqlc.arg(workspace_id) AND revoked_at IS NULL;
