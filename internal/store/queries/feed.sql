-- name: AddFeed :one
INSERT INTO feed (workspace_id, project_id, kind, author, text, screen, record_id, actions, command, params, document_id, revision)
VALUES (sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(author), sqlc.arg(text), sqlc.arg(screen),
    sqlc.narg(record_id), sqlc.arg(actions), sqlc.arg(command), sqlc.arg(params), sqlc.narg(document_id), sqlc.arg(revision))
RETURNING *;


-- name: ListFeed :many
SELECT * FROM feed WHERE workspace_id = sqlc.arg(workspace_id) AND (sqlc.arg(project_id) = 0 OR project_id = sqlc.arg(project_id))
  AND (sqlc.arg(kind) = '' OR kind = sqlc.arg(kind)) AND for_master = 0 AND (CAST(sqlc.arg(unread) AS INTEGER) = 0 OR kind = 'note' AND read_at IS NULL)
ORDER BY id DESC LIMIT sqlc.arg(lim);


-- name: GetFeed :one
SELECT * FROM feed WHERE id = ?;


-- name: MarkFeedUndone :execrows
UPDATE feed SET undone_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id = ? AND undone_at IS NULL;


-- name: PurgeOldNotes :exec
DELETE FROM feed WHERE kind = 'note' AND for_master = 0 AND (read_at IS NOT NULL OR author != 'deck')
  AND created_at < CAST(sqlc.arg(before) AS TEXT);


-- name: AddMasterNote :exec
INSERT INTO feed (workspace_id, project_id, kind, author, text, for_master) VALUES (?, ?, 'note', 'deck', ?, 1);


-- name: ListMasterNotes :many
SELECT * FROM feed WHERE workspace_id = ? AND for_master = 1 ORDER BY id;


-- name: DeleteMasterNotes :exec
DELETE FROM feed WHERE workspace_id = sqlc.arg(workspace_id) AND for_master = 1 AND id <= sqlc.arg(through);


-- name: AddDeckNote :exec
INSERT INTO feed (workspace_id, project_id, kind, author, text)
SELECT j.workspace_id, j.id, 'note', 'deck', sqlc.arg(text) FROM project j WHERE j.id = sqlc.arg(project_id);


-- name: MarkFeedRead :execrows
UPDATE feed SET read_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE id = sqlc.arg(id) AND workspace_id = sqlc.arg(workspace_id) AND kind = 'note' AND for_master = 0 AND read_at IS NULL;


-- name: UnreadDeckNote :one
SELECT * FROM feed WHERE project_id = ? AND kind = 'note' AND author = 'deck' AND for_master = 0 AND read_at IS NULL
  AND substr(text, 1, length(CAST(sqlc.arg(prefix) AS TEXT))) = CAST(sqlc.arg(prefix) AS TEXT)
ORDER BY id DESC LIMIT 1;


-- name: SetFeedText :exec
UPDATE feed SET text = ? WHERE id = ?;
