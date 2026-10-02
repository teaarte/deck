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


-- name: ListFailedAfter :many
SELECT sqlc.embed(p), j.name AS project_name FROM process p JOIN project j ON j.id = p.project_id
WHERE j.workspace_id = sqlc.arg(workspace_id) AND p.state = 'error' AND p.trial = 0 AND j.builtin = 0
  AND (p.finished_at > sqlc.arg(finished_at) OR p.finished_at = sqlc.arg(finished_at) AND p.id > sqlc.arg(id))
ORDER BY p.finished_at, p.id;


-- name: MaxNotificationID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM notification WHERE workspace_id = ?;


-- name: ListNotificationsAfter :many
SELECT sqlc.embed(n), j.name AS project_name FROM notification n JOIN project j ON j.id = n.project_id
WHERE n.workspace_id = ? AND n.id > ? ORDER BY n.id;


-- name: MaxMasterMessageID :one
SELECT CAST(COALESCE(MAX(m.id), 0) AS INTEGER) FROM chat_message m JOIN chat_topic t ON t.id = m.topic_id WHERE t.workspace_id = ?;


-- name: ListMasterMessagesAfter :many
SELECT m.* FROM chat_message m JOIN chat_topic t ON t.id = m.topic_id
WHERE t.workspace_id = ? AND m.id > ? AND m.role = 'master' AND t.deleted_at IS NULL ORDER BY m.id;


-- name: DeleteWorkspacePushSubscriptions :exec
DELETE FROM push_subscription WHERE workspace_id = ?;
