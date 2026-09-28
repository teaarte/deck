-- +goose NO TRANSACTION
-- +goose Up
-- A project name is unique in its workspace; foreign keys are off so the table swap keeps what points at projects.
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE project_new (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    workspace_id  INTEGER NOT NULL DEFAULT 1,
    status        TEXT NOT NULL DEFAULT 'enabled',
    paused        INTEGER NOT NULL DEFAULT 0,
    UNIQUE (workspace_id, name)
);
INSERT INTO project_new (id, name, created_at, workspace_id, status, paused)
    SELECT id, name, created_at, workspace_id, status, paused FROM project;
DROP TABLE project;
ALTER TABLE project_new RENAME TO project;
ALTER TABLE card DROP COLUMN report;
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
SELECT 1;
