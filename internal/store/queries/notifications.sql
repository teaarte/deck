-- name: AddNotification :one
INSERT INTO notification (workspace_id, project_id, author, text, screen, record_id, actions) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;


-- name: ListNotifications :many
SELECT * FROM notification WHERE workspace_id = sqlc.arg(workspace_id) AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
ORDER BY id DESC LIMIT sqlc.arg(lim);
