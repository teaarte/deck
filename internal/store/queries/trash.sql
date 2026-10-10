-- name: AddTrash :one
INSERT INTO trash (workspace_id, project_id, what, record_id, path, records, documents, files, created_at)
VALUES (sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(what), sqlc.narg(record_id), sqlc.arg(path), sqlc.arg(records),
    sqlc.arg(documents), sqlc.arg(files), sqlc.arg(created_at))
RETURNING *;


-- name: SetTrashPaths :exec
UPDATE trash SET paths = ? WHERE id = ?;


-- name: GetTrash :one
SELECT * FROM trash WHERE id = ?;


-- name: LastTrashOf :one
-- The newest trash of a record or of a path of a data folder of a project.
SELECT * FROM trash WHERE project_id = sqlc.arg(project_id)
    AND (record_id = sqlc.narg(record_id) OR (CAST(sqlc.arg(path) AS TEXT) != '' AND path = sqlc.arg(path)))
ORDER BY id DESC LIMIT 1;


-- name: DeleteTrash :exec
DELETE FROM trash WHERE id = ?;


-- name: ListOldTrash :many
SELECT * FROM trash WHERE created_at < ? ORDER BY id;
