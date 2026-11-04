-- name: CreateDevice :one
INSERT INTO device (workspace_id, kind, name, passkey_id, mark, seen_at) VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;


-- name: GetDevice :one
SELECT * FROM device WHERE id = ? AND workspace_id = ?;


-- name: DeviceByMark :one
-- The browser's own device, while it may still sign in.
SELECT * FROM device WHERE mark = ? AND kind = 'browser' AND revoked_at IS NULL;


-- name: ListDevices :many
SELECT sqlc.embed(d), COALESCE(p.name, '') AS passkey_name FROM device d LEFT JOIN passkey p ON p.id = d.passkey_id
WHERE d.workspace_id = ? ORDER BY d.id;


-- name: CountSigned :one
-- The passkeys and nodes that may still sign in, of every workspace.
SELECT (SELECT COUNT(*) FROM passkey WHERE revoked_at IS NULL) + (SELECT COUNT(*) FROM device WHERE kind = 'node' AND revoked_at IS NULL);


-- name: SignDevice :exec
-- A browser signed in again: by the passkey it signed in with, and its mark.
UPDATE device SET passkey_id = ?, mark = ?, seen_at = ? WHERE id = ?;


-- name: RenameDevice :execrows
UPDATE device SET name = ? WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL;


-- name: RevokeDevice :execrows
UPDATE device SET revoked_at = ?, mark = NULL, push_endpoint = NULL, push_p256dh = '', push_auth = ''
WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL;


-- name: CreatePasskey :one
INSERT INTO passkey (workspace_id, name, rp_id, credential_id, user_handle, credential, seen_at) VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING *;


-- name: PasskeyByCredential :one
SELECT * FROM passkey WHERE credential_id = ? AND user_handle = ? AND rp_id = ? AND revoked_at IS NULL;


-- name: PasskeyCredentials :many
-- The credentials a browser may already hold for deck at an address, while they may still sign in.
SELECT credential_id FROM passkey WHERE workspace_id = ? AND rp_id = ? AND revoked_at IS NULL ORDER BY id;


-- name: SetPasskeyCredential :exec
UPDATE passkey SET credential = ?, seen_at = ? WHERE id = ?;


-- name: ListPasskeys :many
SELECT sqlc.embed(p), (SELECT COUNT(*) FROM device d WHERE d.passkey_id = p.id AND d.revoked_at IS NULL) AS devices
FROM passkey p WHERE p.workspace_id = ? ORDER BY p.id;


-- name: RenamePasskey :execrows
UPDATE passkey SET name = ? WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL;


-- name: RevokePasskey :execrows
UPDATE passkey SET revoked_at = ? WHERE id = ? AND workspace_id = ? AND revoked_at IS NULL;


-- name: PasskeyDevices :many
-- The devices a passkey signed in last, while they may still sign in.
SELECT id FROM device WHERE passkey_id = ? AND workspace_id = ? AND revoked_at IS NULL;


-- name: CreateSession :exec
INSERT INTO session (hash, device_id, created_at, seen_at) VALUES (?, ?, ?, ?);


-- name: SessionByHash :one
SELECT s.created_at, s.seen_at, s.settled, sqlc.embed(d) FROM session s JOIN device d ON d.id = s.device_id
WHERE s.hash = ? AND d.revoked_at IS NULL;


-- name: SettleSession :exec
UPDATE session SET device_id = ?, settled = 1 WHERE hash = ?;


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
