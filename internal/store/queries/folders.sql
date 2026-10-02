-- name: AddProjectFolder :one
INSERT INTO project_folder (project_id, name, path) VALUES (?, ?, ?) RETURNING *;


-- name: ListProjectFolders :many
SELECT * FROM project_folder WHERE project_id = ? ORDER BY name;


-- name: RemoveProjectFolder :execrows
DELETE FROM project_folder WHERE project_id = ? AND name = ?;


-- name: DropStaleFolderLocks :exec
DELETE FROM folder_lock WHERE process_id NOT IN (SELECT id FROM process WHERE finished_at IS NULL);


-- name: TakeFolderLock :exec
INSERT INTO folder_lock (path, process_id) VALUES (?, ?) ON CONFLICT (path) DO NOTHING;


-- name: FolderLockHolder :one
SELECT process_id FROM folder_lock WHERE path = ?;


-- name: ReleaseClosedFolderLocks :many
DELETE FROM folder_lock WHERE process_id = ? AND process_id IN (SELECT id FROM process WHERE finished_at IS NOT NULL) RETURNING path;


-- name: ReleaseFolderLocks :exec
DELETE FROM folder_lock WHERE process_id = ?;




-- name: FolderBusy :one
SELECT count(*) FROM folder_lock WHERE path = ?;
