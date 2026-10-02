-- name: AddFeed :one
INSERT INTO feed (workspace_id, project_id, kind, author, text, screen, record_id, actions, command, params, document_id, revision)
VALUES (sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(author), sqlc.arg(text), sqlc.arg(screen),
    sqlc.narg(record_id), sqlc.arg(actions), sqlc.arg(command), sqlc.arg(params), sqlc.narg(document_id), sqlc.arg(revision))
RETURNING *;


-- name: ListFeed :many
SELECT * FROM feed WHERE workspace_id = sqlc.arg(workspace_id) AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
  AND (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind))
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: GetFeed :one
SELECT * FROM feed WHERE id = ?;


-- name: MarkFeedUndone :execrows
UPDATE feed SET undone_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND undone_at IS NULL;


-- name: PurgeOldNotes :exec
DELETE FROM feed WHERE kind = 'note' AND created_at < CAST(sqlc.arg(before) AS TEXT);
