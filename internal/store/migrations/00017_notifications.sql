-- +goose Up
CREATE TABLE notification (
    id            INTEGER PRIMARY KEY,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id),
    project_id    INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    author        TEXT NOT NULL,
    text          TEXT NOT NULL,
    screen        TEXT NOT NULL DEFAULT '',
    record_id     INTEGER NOT NULL DEFAULT 0,
    actions       TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX notification_workspace ON notification (workspace_id, id);

-- +goose Down
DROP TABLE notification;
