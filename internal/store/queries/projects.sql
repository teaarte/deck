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

