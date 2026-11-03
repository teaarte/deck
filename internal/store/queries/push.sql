-- name: DropPushEndpoint :exec
-- An endpoint is one device's: another that had it gives it up.
UPDATE device SET push_endpoint = NULL, push_p256dh = '', push_auth = '' WHERE push_endpoint = sqlc.arg(endpoint) AND id != sqlc.arg(id);


-- name: SetDevicePush :one
UPDATE device SET push_endpoint = ?, push_p256dh = ?, push_auth = ? WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL
RETURNING *;


-- name: ListPushDevices :many
SELECT * FROM device WHERE workspace_id = ? AND push_endpoint IS NOT NULL AND revoked_at IS NULL ORDER BY id;


-- name: DeletePush :execrows
UPDATE device SET push_endpoint = NULL, push_p256dh = '', push_auth = ''
WHERE workspace_id = ? AND push_endpoint IS NOT NULL AND (push_endpoint = sqlc.arg(endpoint) OR id = sqlc.arg(id));


-- name: MaxAskID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM ask WHERE workspace_id = ?;


-- name: ListAsksAfter :many
SELECT * FROM ask WHERE workspace_id = ? AND id > ? AND state = 'open' ORDER BY id;


-- name: MaxNoteID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) FROM feed WHERE workspace_id = ? AND kind = 'note' AND for_master = 0;


-- name: ListNotesAfter :many
SELECT sqlc.embed(n), j.name AS project_name FROM feed n JOIN project j ON j.id = n.project_id
WHERE n.workspace_id = ? AND n.kind = 'note' AND n.for_master = 0 AND n.id > ? ORDER BY n.id;


-- name: DeleteWorkspacePush :exec
UPDATE device SET push_endpoint = NULL, push_p256dh = '', push_auth = '' WHERE workspace_id = ?;


-- name: DeleteStalePushMarks :exec
DELETE FROM mark WHERE mark.workspace_id = sqlc.arg(workspace_id) AND mark.name LIKE 'push:%' AND (NOT EXISTS (
    SELECT 1 FROM device s WHERE s.workspace_id = mark.workspace_id AND s.push_endpoint IS NOT NULL AND mark.name LIKE 'push:' || s.id || ':%')
  OR mark.name NOT LIKE 'push:%:ask' AND mark.name NOT LIKE 'push:%:notify');
