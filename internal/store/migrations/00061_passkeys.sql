-- +goose Up
-- A passkey synced to many browsers signs each in as a device of its own, which keeps the sha256 of its browser's mark;
-- a passkey keeps the id of its device, and the sessions of before settle on their browsers at their next request.
CREATE TABLE passkey (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    rp_id TEXT NOT NULL,
    credential_id BLOB NOT NULL UNIQUE,
    user_handle BLOB NOT NULL,
    credential TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    seen_at TEXT,
    revoked_at TEXT
);
INSERT INTO passkey (id, workspace_id, name, rp_id, credential_id, user_handle, credential, created_at, seen_at, revoked_at)
SELECT id, workspace_id, name, rp_id, credential_id, COALESCE(user_handle, X''), credential, created_at, seen_at, revoked_at
FROM device WHERE credential_id IS NOT NULL;

CREATE TABLE device_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('browser', 'node')),
    name TEXT NOT NULL,
    passkey_id INTEGER REFERENCES passkey(id),
    mark TEXT UNIQUE,
    push_endpoint TEXT UNIQUE,
    push_p256dh TEXT NOT NULL DEFAULT '',
    push_auth TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    seen_at TEXT,
    revoked_at TEXT
);
INSERT INTO device_new (id, workspace_id, kind, name, passkey_id, push_endpoint, push_p256dh, push_auth, created_at, seen_at, revoked_at)
SELECT id, workspace_id, kind, name, CASE WHEN credential_id IS NOT NULL THEN id END, push_endpoint, push_p256dh, push_auth,
    created_at, seen_at, revoked_at
FROM device;
-- No id of a device comes again.
DELETE FROM sqlite_sequence WHERE name = 'device_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'device_new', seq FROM sqlite_sequence WHERE name = 'device';

CREATE TABLE session_new (
    hash TEXT PRIMARY KEY,
    device_id INTEGER NOT NULL REFERENCES device_new(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    seen_at TEXT NOT NULL,
    settled INTEGER NOT NULL DEFAULT 1
);
INSERT INTO session_new (hash, device_id, created_at, seen_at, settled) SELECT hash, device_id, created_at, seen_at, 0 FROM session;

DROP TABLE session;
DROP TABLE device;
ALTER TABLE device_new RENAME TO device;
ALTER TABLE session_new RENAME TO session;
CREATE INDEX session_device ON session (device_id);
CREATE INDEX device_passkey ON device (passkey_id);

-- +goose Down
SELECT 1;
