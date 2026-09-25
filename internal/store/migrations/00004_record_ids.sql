-- +goose Up
-- AUTOINCREMENT keeps ids of deleted records from being reused by documents and cards that point at them.
CREATE TABLE record_new (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    collection  TEXT NOT NULL,
    key         TEXT NOT NULL,
    data        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, collection, key)
);
INSERT INTO record_new SELECT id, project_id, collection, key, data, created_at, updated_at FROM record;
DROP TABLE record;
ALTER TABLE record_new RENAME TO record;

-- +goose Down
SELECT 1;
