-- name: CreateDevice :one
INSERT INTO device (workspace_id, kind, name, rp_id, credential_id, user_handle, credential, seen_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;


-- name: GetDevice :one
SELECT * FROM device WHERE id = ? AND workspace_id = ?;


-- name: DeviceByCredential :one
SELECT * FROM device WHERE credential_id = ? AND user_handle = ? AND rp_id = ? AND revoked_at IS NULL;


-- name: ListDevices :many
SELECT * FROM device WHERE workspace_id = ? ORDER BY id;


-- name: CountSignedDevices :one
-- The devices that may still sign in, of every workspace.
SELECT COUNT(*) FROM device WHERE revoked_at IS NULL AND kind != 'browser' OR revoked_at IS NULL AND credential_id IS NOT NULL;


-- name: SetDeviceCredential :exec
UPDATE device SET credential = ?, seen_at = ? WHERE id = ?;


-- name: RenameDevice :execrows
UPDATE device SET name = ? WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL;


-- name: RevokeDevice :execrows
UPDATE device SET revoked_at = ?, push_endpoint = NULL, push_p256dh = '', push_auth = ''
WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL;


-- name: DeleteKindSessions :exec
DELETE FROM session WHERE device_id IN (SELECT id FROM device WHERE workspace_id = ? AND kind = ?);


-- name: DeleteKind :many
-- Deletes the devices of a kind, such as the launcher before a new one.
DELETE FROM device WHERE workspace_id = ? AND kind = ?
RETURNING id;


-- name: CreateSession :exec
INSERT INTO session (hash, device_id, created_at, seen_at) VALUES (?, ?, ?, ?);


-- name: SessionByHash :one
SELECT s.created_at, s.seen_at, sqlc.embed(d) FROM session s JOIN device d ON d.id = s.device_id
WHERE s.hash = ? AND d.revoked_at IS NULL;


-- name: TouchSession :exec
UPDATE session SET seen_at = sqlc.arg(seen_at) WHERE hash = sqlc.arg(hash);


-- name: TouchDevice :exec
UPDATE device SET seen_at = ? WHERE id = ?;


-- name: DeleteSession :exec
DELETE FROM session WHERE hash = ?;


-- name: DeleteDeviceSessions :exec
DELETE FROM session WHERE device_id = ?;


-- name: DeleteSessionsBefore :exec
-- The sessions no request can renew any more: seen before the first time, or begun before the second.
DELETE FROM session WHERE seen_at < sqlc.arg(seen) OR created_at < sqlc.arg(created);
