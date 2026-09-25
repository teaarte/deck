-- +goose NO TRANSACTION
-- +goose Up
-- AUTOINCREMENT keeps ids of deleted documents from pointing at new ones; foreign keys are off so the
-- table swap keeps revisions.
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE document_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL DEFAULT 1 REFERENCES workspace(id),
    project_id    INTEGER NOT NULL DEFAULT 0,
    owner_kind    TEXT NOT NULL DEFAULT '',
    owner_id      INTEGER NOT NULL DEFAULT 0,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    current       INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (workspace_id, project_id, owner_kind, owner_id, name)
);
INSERT INTO document_new SELECT id, workspace_id, project_id, owner_kind, owner_id, kind, name, current, created_at FROM document;
DROP TABLE document;
ALTER TABLE document_new RENAME TO document;
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
SELECT 1;
