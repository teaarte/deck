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


-- name: PurgeOrphanFiles :exec
DELETE FROM file
WHERE (owner_kind = 'record' AND owner_id NOT IN (SELECT id FROM record))
   OR (owner_kind = 'process' AND owner_id NOT IN (SELECT id FROM process))
   OR (owner_kind = 'chat_message' AND owner_id NOT IN (SELECT id FROM chat_message));


-- name: PurgeTrash :exec
-- A deleted file waits in the trash with the second it went there as its owner.
DELETE FROM file WHERE owner_kind = 'trash' AND owner_id < sqlc.arg(before);


-- name: PurgeStaleUploads :exec
DELETE FROM file WHERE owner_kind IN ('upload', 'keeping') AND created_at < CAST(sqlc.arg(before) AS TEXT);


-- name: BindFile :execrows
UPDATE file SET owner_kind = sqlc.arg(owner_kind), owner_id = sqlc.arg(owner_id)
WHERE id = sqlc.arg(id) AND project_id = sqlc.arg(project_id) AND owner_kind = sqlc.arg(from_kind) AND owner_id = sqlc.arg(from_id);
-- name: DeleteExpiredRecordFiles :exec
DELETE FROM file WHERE owner_kind = 'record' AND created_at < CAST(sqlc.arg(before) AS TEXT)
AND owner_id IN (SELECT r.id FROM record r WHERE r.project_id = sqlc.arg(project_id) AND r.collection = sqlc.arg(collection));




-- name: ListWorkspaceFiles :many
SELECT * FROM file WHERE project_id IN (SELECT id FROM project WHERE workspace_id = sqlc.arg(workspace_id))
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = CAST(sqlc.arg(project_id) AS INTEGER)) AND owner_kind != 'trash'
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: ListMessageFiles :many
SELECT f.* FROM file f JOIN chat_message m ON m.id = f.owner_id
WHERE f.owner_kind = 'chat_message' AND m.topic_id = ? ORDER BY f.id;


-- name: DeleteFilesOf :exec
DELETE FROM file WHERE owner_kind = ? AND owner_id = ?;


-- name: ListProjectFileIDs :many
SELECT id FROM file WHERE project_id = ? ORDER BY id;
