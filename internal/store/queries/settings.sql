-- name: GetSetting :one
SELECT value FROM setting WHERE workspace_id = sqlc.arg(workspace_id) AND key = sqlc.arg(key);


-- name: SetSetting :exec
INSERT INTO setting (workspace_id, key, value) VALUES (sqlc.arg(workspace_id), sqlc.arg(key), sqlc.arg(value))
ON CONFLICT (workspace_id, key) DO UPDATE SET value = excluded.value;


-- name: GetMark :one
SELECT value FROM mark WHERE workspace_id = ? AND name = ?;


-- name: SetMark :exec
INSERT INTO mark (workspace_id, project_id, name, value) VALUES (sqlc.arg(workspace_id), sqlc.narg(project_id), sqlc.arg(name), sqlc.arg(value))
ON CONFLICT (workspace_id, name) DO UPDATE SET value = excluded.value;


-- name: DeleteMarksWithPrefix :exec
DELETE FROM mark WHERE workspace_id = sqlc.arg(workspace_id) AND substr(name, 1, length(CAST(sqlc.arg(prefix) AS TEXT))) = CAST(sqlc.arg(prefix) AS TEXT);


-- name: DeleteMarksOfOtherRevisions :exec
DELETE FROM mark WHERE workspace_id = sqlc.arg(workspace_id)
  AND substr(name, 1, length(CAST(sqlc.arg(prefix) AS TEXT))) = CAST(sqlc.arg(prefix) AS TEXT)
  AND substr(name, -length(CAST(sqlc.arg(revision) AS TEXT))) != CAST(sqlc.arg(revision) AS TEXT);
