-- +goose Up
CREATE TABLE automation_token (
    id            INTEGER PRIMARY KEY,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id),
    name          TEXT NOT NULL,
    hash          TEXT NOT NULL UNIQUE,
    commands      TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at    TEXT
);

-- command and params undo the change as the owner; document_id and revision guard against later edits.
CREATE TABLE unattended (
    id            INTEGER PRIMARY KEY,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id),
    project_id    INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    author        TEXT NOT NULL,
    text          TEXT NOT NULL,
    command       TEXT NOT NULL DEFAULT '',
    params        TEXT NOT NULL DEFAULT '{}',
    document_id   INTEGER NOT NULL DEFAULT 0,
    revision      INTEGER NOT NULL DEFAULT 0,
    undone_at     TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

ALTER TABLE process ADD COLUMN trial INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tool_call ADD COLUMN actor TEXT NOT NULL DEFAULT '';

CREATE TABLE ask_new (
    id            INTEGER PRIMARY KEY,
    process_id    INTEGER REFERENCES process(id) ON DELETE CASCADE,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id),
    project_id    INTEGER NOT NULL,
    kind          TEXT NOT NULL,
    text          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    answer        TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'open',
    delivered     INTEGER NOT NULL DEFAULT 0,
    used          INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    answered_at   TEXT,
    expires_at    TEXT
);
INSERT INTO ask_new (id, process_id, workspace_id, project_id, kind, text, payload, answer, state, delivered, used, created_at, answered_at, expires_at)
SELECT a.id, a.process_id, j.workspace_id, j.id, a.kind, a.text, a.payload, a.answer, a.state, a.delivered, a.used, a.created_at, a.answered_at, a.expires_at
FROM ask a JOIN process p ON p.id = a.process_id JOIN project j ON j.id = p.project_id;
DROP TABLE ask;
ALTER TABLE ask_new RENAME TO ask;
CREATE INDEX ask_process ON ask (process_id, state);
CREATE INDEX ask_workspace ON ask (workspace_id, state);

-- +goose Down
CREATE TABLE ask_old (
    id           INTEGER PRIMARY KEY,
    process_id   INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    text         TEXT NOT NULL,
    payload      TEXT NOT NULL DEFAULT '{}',
    answer       TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL DEFAULT 'open',
    delivered    INTEGER NOT NULL DEFAULT 0,
    used         INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    answered_at  TEXT,
    expires_at   TEXT
);
INSERT INTO ask_old SELECT id, process_id, kind, text, payload, answer, state, delivered, used, created_at, answered_at, expires_at
FROM ask WHERE process_id IS NOT NULL;
DROP TABLE ask;
ALTER TABLE ask_old RENAME TO ask;
CREATE INDEX ask_process ON ask (process_id, state);
ALTER TABLE tool_call DROP COLUMN actor;
ALTER TABLE process DROP COLUMN trial;
DROP TABLE unattended;
DROP TABLE automation_token;
