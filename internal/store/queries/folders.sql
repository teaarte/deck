-- name: AddProjectFolder :one
INSERT INTO project_folder (project_id, name, path) VALUES (?, ?, ?) RETURNING *;


-- name: ListProjectFolders :many
SELECT * FROM project_folder WHERE project_id = ? ORDER BY name;


-- name: RemoveProjectFolder :execrows
DELETE FROM project_folder WHERE project_id = ? AND name = ?;


-- name: DropStaleFolderLock :exec
DELETE FROM folder_lock
WHERE path = sqlc.arg(path) AND process_id NOT IN (SELECT id FROM process
  WHERE state IN ('running', 'waiting_answer', 'needs_login', 'rate_limited', 'waiting_provider', 'waiting_slot'));


-- name: TakeFolderLock :exec
INSERT INTO folder_lock (path, process_id) VALUES (?, ?) ON CONFLICT (path) DO NOTHING;


-- name: FolderLockHolder :one
SELECT process_id FROM folder_lock WHERE path = ?;


-- name: ReleaseFolderLocks :exec
DELETE FROM folder_lock WHERE process_id = ?;


-- name: WaitForFolder :execrows
UPDATE process SET state = 'waiting_folder', wait_message = sqlc.arg(message), wait_fresh = sqlc.arg(fresh)
WHERE id = sqlc.arg(id) AND state = 'running';


-- name: ListWaitingForFolder :many
SELECT * FROM process WHERE state = 'waiting_folder' AND folder = ? ORDER BY id;


-- name: ListWaitingFolders :many
SELECT DISTINCT folder FROM process WHERE state = 'waiting_folder';
