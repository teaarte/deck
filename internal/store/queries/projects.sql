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


-- name: DeleteProject :execrows
DELETE FROM project WHERE id = ?;
-- name: ProjectPulses :many
SELECT p.id,
  CAST(coalesce(r.n, 0) AS INTEGER) AS running,
  CAST(coalesce(a.n, 0) AS INTEGER) AS asks,
  CAST(coalesce(h.n, 0) AS INTEGER) AS errors
FROM project p
LEFT JOIN (SELECT x.project_id, count(*) AS n FROM process x WHERE x.state = 'running' AND x.finished_at IS NULL GROUP BY x.project_id) r ON r.project_id = p.id
LEFT JOIN (SELECT y.project_id, count(*) AS n FROM ask y WHERE y.workspace_id = sqlc.arg(workspace_id) AND y.state = 'open' GROUP BY y.project_id) a ON a.project_id = p.id
LEFT JOIN (SELECT z.project_id, count(*) AS n FROM health z WHERE z.workspace_id = sqlc.arg(workspace_id) AND z.status = 'error'
    AND (z.seen_at IS NULL OR z.seen_at < z.updated_at) GROUP BY z.project_id) h ON h.project_id = p.id
WHERE p.workspace_id = sqlc.arg(workspace_id);


-- name: GrantProject :exec
UPDATE project SET granted_revision = ? WHERE id = ?;


-- name: SetImportedFrom :exec
UPDATE project SET imported_from = ? WHERE id = ?;

