-- name: ListGrants :many
SELECT * FROM right_grant WHERE project_id = ? ORDER BY name, who, subject;


-- name: ListWorkspaceGrants :many
SELECT g.* FROM right_grant g JOIN project p ON p.id = g.project_id WHERE p.workspace_id = ? ORDER BY g.project_id, g.name, g.who, g.subject;


-- name: SetGrant :exec
INSERT INTO right_grant (project_id, name, who, subject, answer, print, device_id, created_at)
VALUES (sqlc.arg(project_id), sqlc.arg(name), sqlc.arg(who), sqlc.arg(subject), sqlc.arg(answer), sqlc.arg(print), sqlc.arg(device_id), sqlc.arg(created_at))
ON CONFLICT (project_id, name, who, subject) DO UPDATE SET answer = excluded.answer, print = excluded.print,
    device_id = excluded.device_id, created_at = excluded.created_at;


-- name: DeleteGrant :execrows
DELETE FROM right_grant WHERE project_id = ? AND name = ? AND who = ? AND subject = ?;
