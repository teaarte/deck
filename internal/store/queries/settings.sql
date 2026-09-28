-- name: GetSetting :one
SELECT value FROM setting WHERE workspace_id = sqlc.arg(workspace_id) AND key = sqlc.arg(key);


-- name: SetSetting :exec
INSERT INTO setting (workspace_id, key, value) VALUES (sqlc.arg(workspace_id), sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (workspace_id, key) DO UPDATE SET value = excluded.value;


-- name: DeleteSetting :execrows
DELETE FROM setting WHERE workspace_id = sqlc.arg(workspace_id) AND key = sqlc.arg(key);


-- name: ListSettings :many
SELECT key, value FROM setting WHERE workspace_id = sqlc.arg(workspace_id) AND key LIKE CAST(sqlc.arg(prefix) AS TEXT) || '%' ORDER BY key;




-- name: DeleteSettingsWithPrefix :exec
DELETE FROM setting WHERE workspace_id = sqlc.arg(workspace_id) AND key LIKE CAST(sqlc.arg(prefix) AS TEXT) || '%';
