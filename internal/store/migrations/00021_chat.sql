-- +goose Up
-- builtin marks the project deck seeds itself: it cannot be deleted, and its agents act as the master.
ALTER TABLE project ADD COLUMN builtin INTEGER NOT NULL DEFAULT 0;

CREATE TABLE chat_topic (
    id            INTEGER PRIMARY KEY,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id),
    title         TEXT NOT NULL,
    card_id       INTEGER NOT NULL DEFAULT 0,
    archived      INTEGER NOT NULL DEFAULT 0,
    deleted_at    TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX chat_topic_workspace ON chat_topic (workspace_id, updated_at);

-- seen is the last message of the topic the session was told about.
CREATE TABLE chat_session (
    topic_id    INTEGER NOT NULL REFERENCES chat_topic(id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    seen        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (topic_id, provider)
);

CREATE TABLE chat_message (
    id          INTEGER PRIMARY KEY,
    topic_id    INTEGER NOT NULL REFERENCES chat_topic(id) ON DELETE CASCADE,
    role        TEXT NOT NULL,
    text        TEXT NOT NULL,
    context     TEXT NOT NULL DEFAULT '',
    process_id  INTEGER NOT NULL DEFAULT 0,
    retry_key   TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX chat_message_topic ON chat_message (topic_id, id);
CREATE INDEX chat_message_process ON chat_message (process_id);

-- +goose Down
DROP TABLE chat_message;
DROP TABLE chat_session;
DROP TABLE chat_topic;
ALTER TABLE project DROP COLUMN builtin;
