-- name: CreateProject :one
INSERT INTO project (workspace_id, name, status) VALUES (?, ?, ?) RETURNING *;


-- name: GetProject :one
SELECT * FROM project WHERE id = ?;


-- name: GetProjectByName :one
SELECT * FROM project WHERE name = ?;


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
