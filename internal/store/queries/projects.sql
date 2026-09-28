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


-- name: DeleteProjectFiles :many
DELETE FROM file WHERE project_id = ? RETURNING id;


-- name: DeleteProjectDocuments :exec
DELETE FROM document WHERE workspace_id = sqlc.arg(workspace_id) AND project_id = sqlc.arg(project_id) AND project_id != 0;


-- name: DeleteProjectAsks :exec
DELETE FROM ask WHERE project_id = ?;


-- name: DeleteProjectHealth :exec
DELETE FROM health
WHERE subject LIKE 'command:' || CAST(sqlc.arg(name) AS TEXT) || '.%'
   OR subject LIKE 'tool:' || CAST(sqlc.arg(name) AS TEXT) || '.%'
   OR subject LIKE 'mcp:' || CAST(sqlc.arg(name) AS TEXT) || '.%';


-- name: DeleteProject :execrows
DELETE FROM project WHERE id = ?;
