-- name: AddPushSubscription :one
INSERT INTO push_subscription (workspace_id, device, endpoint, p256dh, auth) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (endpoint) DO UPDATE SET workspace_id = excluded.workspace_id, device = excluded.device, p256dh = excluded.p256dh, auth = excluded.auth
RETURNING *;


-- name: ListPushSubscriptions :many
SELECT * FROM push_subscription WHERE workspace_id = ? ORDER BY id;


-- name: DeletePushSubscription :execrows
DELETE FROM push_subscription WHERE workspace_id = ? AND (endpoint = sqlc.arg(endpoint) OR id = sqlc.arg(id));


-- name: MaxAskID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM ask WHERE workspace_id = ?;


-- name: ListAsksAfter :many
SELECT * FROM ask WHERE workspace_id = ? AND id > ? AND state = 'open' ORDER BY id;


-- name: MaxNoteID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM feed WHERE workspace_id = ? AND kind = 'note' AND for_master = 0;


-- name: ListNotesAfter :many
SELECT sqlc.embed(n), j.name AS project_name FROM feed n JOIN project j ON j.id = n.project_id
WHERE n.workspace_id = ? AND n.kind = 'note' AND n.for_master = 0 AND n.id > ? ORDER BY n.id;


-- name: DeleteWorkspacePushSubscriptions :exec
DELETE FROM push_subscription WHERE workspace_id = ?;


-- name: DeleteStalePushMarks :exec
DELETE FROM mark WHERE mark.workspace_id = sqlc.arg(workspace_id) AND mark.name LIKE 'push:%' AND (NOT EXISTS (
    SELECT 1 FROM push_subscription s WHERE s.workspace_id = mark.workspace_id AND mark.name LIKE 'push:' || s.id || ':%')
  OR mark.name NOT LIKE 'push:%:ask' AND mark.name NOT LIKE 'push:%:notify');
