-- name: AddUnattended :exec
INSERT INTO unattended (workspace_id, project_id, author, text, command, params, document_id, revision) VALUES (?, ?, ?, ?, ?, ?, ?, ?);


-- name: ListUnattended :many
SELECT * FROM unattended WHERE workspace_id = sqlc.arg(workspace_id) AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: GetUnattended :one
SELECT * FROM unattended WHERE id = ?;


-- name: MarkUnattendedUndone :execrows
UPDATE unattended SET undone_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND undone_at IS NULL;
