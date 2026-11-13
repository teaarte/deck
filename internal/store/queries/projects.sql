-- name: CreateProject :one
INSERT INTO project (workspace_id, name, status) VALUES (?, ?, ?) RETURNING *;


-- name: GetProject :one
SELECT * FROM project WHERE id = ?;


-- name: GetProjectByName :one
SELECT * FROM project WHERE workspace_id = ? AND name = ?;


-- name: ListProjects :many
SELECT * FROM project ORDER BY name;


-- name: ListWorkspaceProjects :many
SELECT * FROM project WHERE workspace_id = ? ORDER BY name;



-- name: SetProjectPaused :exec
UPDATE project SET paused = ? WHERE id = ?;


-- name: SetProjectStatus :execrows
UPDATE project SET status = sqlc.arg(status) WHERE id = sqlc.arg(id) AND status != sqlc.arg(status);


-- name: ListWorkspaces :many
SELECT id FROM workspace ORDER BY id;


-- name: DeleteProjectFiles :exec
DELETE FROM file WHERE project_id = ?;


-- name: DeleteProject :execrows
DELETE FROM project WHERE id = ?;


-- name: SetImportedFrom :exec
UPDATE project SET imported_from = ? WHERE id = ?;



-- name: MarkProjectUntrusted :exec
-- The data of a project holds untrusted text: what wrote it, the first that marked it.
UPDATE project SET untrusted = ? WHERE id = ? AND untrusted = '';


-- name: ClearProjectUntrusted :execrows
UPDATE project SET untrusted = '' WHERE id = ? AND workspace_id = ? AND untrusted != '';


-- name: FirstUntrustedProject :one
-- The first project of a workspace whose data holds untrusted text.
SELECT id, name, untrusted FROM project WHERE workspace_id = ? AND untrusted != '' ORDER BY id LIMIT 1;
