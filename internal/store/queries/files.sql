-- name: CreateFile :one
INSERT INTO file (project_id, owner_kind, owner_id, name, type, size, created_at)
VALUES (sqlc.arg(project_id), sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(name), sqlc.arg(type), sqlc.arg(size), sqlc.arg(created_at))
RETURNING *;


-- name: GetFile :one
SELECT * FROM file WHERE id = ?;


-- name: ListFilesOf :many
SELECT * FROM file WHERE project_id = ? AND owner_kind = ? AND owner_id = ? ORDER BY id;


-- name: TotalFileSize :one
SELECT CAST(COALESCE(SUM(size), 0) AS INTEGER) FROM file;


-- name: DeleteFile :exec
DELETE FROM file WHERE id = ?;


-- name: PurgeOrphanFiles :many
DELETE FROM file
WHERE (owner_kind = 'record' AND owner_id NOT IN (SELECT id FROM record))
   OR (owner_kind = 'card' AND owner_id NOT IN (SELECT id FROM card))
   OR (owner_kind = 'process' AND owner_id NOT IN (SELECT id FROM process))
RETURNING id;


-- name: PurgeStaleUploads :many
DELETE FROM file WHERE owner_kind = 'upload' AND created_at < CAST(sqlc.arg(before) AS TEXT) RETURNING id;


-- name: BindFile :execrows
UPDATE file SET owner_kind = sqlc.arg(owner_kind), owner_id = sqlc.arg(owner_id)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id) AND owner_kind = sqlc.arg(from_kind) AND owner_id = sqlc.arg(from_id);
