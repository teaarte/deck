-- +goose Up
-- What the owner deleted with its parts, kept a day for an undo: records with their documents, the owners of
-- files moved to the trash, and paths of a data folder moved under state/trash/<id>.
CREATE TABLE trash (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id   INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    what         TEXT NOT NULL,
    record_id    INTEGER,
    path         TEXT NOT NULL DEFAULT '',
    records      TEXT NOT NULL DEFAULT '[]',
    documents    TEXT NOT NULL DEFAULT '[]',
    files        TEXT NOT NULL DEFAULT '[]',
    paths        TEXT NOT NULL DEFAULT '[]',
    created_at   TEXT NOT NULL
);
CREATE INDEX trash_project ON trash (project_id, created_at);
