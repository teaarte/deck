-- +goose Up
CREATE TABLE project (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    manifest    TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE record (
    id          INTEGER PRIMARY KEY,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    collection  TEXT NOT NULL,
    key         TEXT NOT NULL,
    data        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, collection, key)
);

CREATE TABLE process (
    id             INTEGER PRIMARY KEY,
    project_id     INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command        TEXT NOT NULL,
    state          TEXT NOT NULL,
    provider       TEXT NOT NULL DEFAULT '',
    session_id     TEXT NOT NULL DEFAULT '',
    input          TEXT NOT NULL DEFAULT '{}',
    result         TEXT NOT NULL DEFAULT '',
    not_by_schema  INTEGER NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    started_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at    TEXT
);

CREATE TABLE ask (
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
    answered_at  TEXT
);

CREATE INDEX ask_process ON ask (process_id, state);

CREATE TABLE tool_call (
    id          INTEGER PRIMARY KEY,
    process_id  INTEGER NOT NULL,
    project_id  INTEGER NOT NULL,
    tool        TEXT NOT NULL,
    class       TEXT NOT NULL,
    outcome     TEXT NOT NULL,
    params      TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE trigger_state (
    project_id   INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command      TEXT NOT NULL,
    cron         TEXT NOT NULL,
    next_run_at  TEXT NOT NULL,
    PRIMARY KEY (project_id, command, cron)
);

-- +goose Down
DROP TABLE trigger_state;
DROP TABLE tool_call;
DROP TABLE ask;
DROP TABLE process;
DROP TABLE record;
DROP TABLE project;
