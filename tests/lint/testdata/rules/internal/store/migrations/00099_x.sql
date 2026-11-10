-- +goose Up
CREATE TABLE note (
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO note (created_at) VALUES (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
