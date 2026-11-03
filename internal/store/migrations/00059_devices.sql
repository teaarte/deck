-- +goose Up
-- A device of the owner: a browser with its passkey under a user handle of its own, the window of the launcher or a
-- node; a push reaches it by its subscription. The push subscriptions before become devices with no passkey.
CREATE TABLE device (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('browser', 'launcher', 'node')),
    name TEXT NOT NULL,
    rp_id TEXT NOT NULL DEFAULT '',
    credential_id BLOB UNIQUE,
    user_handle BLOB,
    credential TEXT NOT NULL DEFAULT '',
    push_endpoint TEXT UNIQUE,
    push_p256dh TEXT NOT NULL DEFAULT '',
    push_auth TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    seen_at TEXT,
    revoked_at TEXT
);

INSERT INTO device (id, workspace_id, kind, name, push_endpoint, push_p256dh, push_auth, created_at)
SELECT id, workspace_id, 'browser', device, endpoint, p256dh, auth, created_at FROM push_subscription;
DROP TABLE push_subscription;

-- A signed-in session of a device, by the sha256 of its cookie.
CREATE TABLE session (
    hash TEXT PRIMARY KEY,
    device_id INTEGER NOT NULL REFERENCES device(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    seen_at TEXT NOT NULL
);

CREATE INDEX session_device ON session (device_id);

-- +goose Down
SELECT 1;
