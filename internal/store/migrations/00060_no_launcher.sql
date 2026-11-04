-- +goose Up
-- The launcher is gone: its devices and sessions go, and a device is a browser or a node. The session table is made
-- again beside the device table, so dropping the old one deletes no session of a browser.
CREATE TABLE device_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('browser', 'node')),
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
INSERT INTO device_new SELECT * FROM device WHERE kind != 'launcher';
-- No id of a device comes again.
DELETE FROM sqlite_sequence WHERE name = 'device_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'device_new', seq FROM sqlite_sequence WHERE name = 'device';

CREATE TABLE session_new (
    hash TEXT PRIMARY KEY,
    device_id INTEGER NOT NULL REFERENCES device_new(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    seen_at TEXT NOT NULL
);
INSERT INTO session_new SELECT s.* FROM session s JOIN device_new d ON d.id = s.device_id;

DROP TABLE session;
DROP TABLE device;
ALTER TABLE device_new RENAME TO device;
ALTER TABLE session_new RENAME TO session;
CREATE INDEX session_device ON session (device_id);

-- +goose Down
SELECT 1;
