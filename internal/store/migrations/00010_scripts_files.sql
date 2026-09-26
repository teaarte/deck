-- +goose Up
-- kind is the executor of the process: agent or script.
ALTER TABLE process ADD COLUMN kind TEXT NOT NULL DEFAULT 'agent';

-- A file lives in files/<id>; owner_kind is record, card or process.
CREATE TABLE file (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    owner_kind  TEXT NOT NULL,
    owner_id    INTEGER NOT NULL,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX file_owner ON file (owner_kind, owner_id);

-- records holds JSONL of {"id", "data"} of a collection as it was under manifest revision from_revision.
CREATE TABLE record_snapshot (
    id             INTEGER PRIMARY KEY,
    project_id     INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    collection     TEXT NOT NULL,
    from_revision  INTEGER NOT NULL,
    revision       INTEGER NOT NULL,
    records        TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX record_snapshot_from ON record_snapshot (project_id, collection, from_revision);

-- +goose Down
DROP TABLE record_snapshot;
DROP TABLE file;
