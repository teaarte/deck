-- +goose NO TRANSACTION
-- +goose Up
-- An ask without a project waits at the level of the workspace, as a call of a token to a builtin does.
PRAGMA foreign_keys = OFF;
BEGIN;
CREATE TABLE ask_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER REFERENCES project(id) ON DELETE CASCADE,
    process_id    INTEGER REFERENCES process(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    text          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    answer        TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'open',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    answered_at   TEXT,
    expires_at    TEXT,
    delivered_at  TEXT
);
INSERT INTO ask_new (id, workspace_id, project_id, process_id, kind, text, payload, answer, state, created_at, answered_at, expires_at, delivered_at)
SELECT id, workspace_id, project_id, process_id, kind, text, payload, answer, state, created_at, answered_at, expires_at, delivered_at FROM ask;
DROP TABLE ask;
ALTER TABLE ask_new RENAME TO ask;
CREATE INDEX ask_process ON ask (process_id, state);
CREATE INDEX ask_workspace ON ask (workspace_id, state);
COMMIT;
PRAGMA foreign_keys = ON;
