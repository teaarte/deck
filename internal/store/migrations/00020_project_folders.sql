-- +goose Up
-- path is the real path of the folder, with symbolic links resolved.
CREATE TABLE project_folder (
    id          INTEGER PRIMARY KEY,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, name),
    UNIQUE (project_id, path)
);

CREATE TABLE folder_lock (
    path        TEXT PRIMARY KEY,
    process_id  INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE
);

ALTER TABLE process ADD COLUMN folder TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE process DROP COLUMN folder;
DROP TABLE folder_lock;
DROP TABLE project_folder;
