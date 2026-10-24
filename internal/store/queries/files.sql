-- name: CreateFile :one
INSERT INTO file (project_id, owner_kind, owner_id, name, type, size, created_at, process_id)
VALUES (sqlc.arg(project_id), sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(name), sqlc.arg(type), sqlc.arg(size), sqlc.arg(created_at),
        sqlc.narg(process_id))
RETURNING *;


-- name: GetFile :one
SELECT * FROM file WHERE id = ?;


-- name: ListFilesOf :many
SELECT * FROM file WHERE project_id = ? AND owner_kind = ? AND owner_id = ? ORDER BY id;


-- name: TotalFileSize :one
SELECT CAST(COALESCE(SUM(size), 0) AS INTEGER) FROM file;


-- name: DeleteFile :exec
DELETE FROM file WHERE id = ?;


-- name: PurgeOrphanFiles :exec
DELETE FROM file
WHERE (owner_kind = 'record' AND owner_id NOT IN (SELECT id FROM record))
   OR (owner_kind = 'process' AND owner_id NOT IN (SELECT id FROM process))
   OR (owner_kind = 'chat_message' AND owner_id NOT IN (SELECT id FROM chat_message))
   OR (owner_kind = 'model' AND owner_id NOT IN (SELECT id FROM model));


-- name: PurgeTrash :exec
-- A deleted file waits in the trash with the second it went there as its owner.
DELETE FROM file WHERE owner_kind = 'trash' AND owner_id < sqlc.arg(before);


-- name: PurgeStaleUploads :exec
DELETE FROM file WHERE owner_kind IN ('upload', 'keeping') AND created_at < CAST(sqlc.arg(before) AS TEXT);


-- name: BindFile :execrows
UPDATE file SET owner_kind = sqlc.arg(owner_kind), owner_id = sqlc.arg(owner_id)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id) AND owner_kind = sqlc.arg(from_kind) AND owner_id = sqlc.arg(from_id);




-- name: ListWorkspaceFiles :many
SELECT * FROM file WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = CAST(sqlc.arg(project_id) AS INTEGER)) AND owner_kind NOT IN ('trash', 'keeping')
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: ListMessageFiles :many
SELECT f.* FROM file f JOIN chat_message m ON m.id = f.owner_id
WHERE f.owner_kind = 'chat_message' AND m.topic_id = ? ORDER BY f.id;


-- name: DeleteFilesOf :exec
DELETE FROM file WHERE owner_kind = ? AND owner_id = ?;


-- name: ListProjectFileIDs :many
SELECT id FROM file WHERE project_id = ? ORDER BY id;


-- name: ListMadeFiles :many
-- The files a process kept and those of the processes it started.
SELECT f.* FROM file f WHERE f.owner_kind = 'process'
    AND (f.owner_id = sqlc.arg(process_id) OR f.owner_id IN (SELECT p.id FROM process p WHERE p.parent_id = sqlc.arg(process_id)))
ORDER BY f.id;


-- name: ListOwnedFiles :many
SELECT * FROM file WHERE owner_kind = ? AND owner_id = ? ORDER BY id;
