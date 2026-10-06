-- +goose NO TRANSACTION
-- +goose Up
-- deck keeps no boards: cards and their lane triggers go, documents of cards with them, and their files are left
-- to the sweep of files that lost their owner; documents and topics lose the column of a card.
PRAGMA foreign_keys = OFF;
BEGIN;
DELETE FROM "trigger" WHERE kind = 'lane';
DELETE FROM document_revision WHERE document_id IN (SELECT id FROM document WHERE card_id IS NOT NULL);
DELETE FROM document WHERE card_id IS NOT NULL;
UPDATE file SET owner_kind = 'record', owner_id = 0 WHERE owner_kind = 'card';
CREATE TABLE document_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER REFERENCES project(id) ON DELETE CASCADE,
    record_id     INTEGER,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    current       INTEGER NOT NULL DEFAULT 0,
    deleted_at    TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO document_new (id, workspace_id, project_id, record_id, kind, name, current, deleted_at, created_at)
SELECT id, workspace_id, project_id, record_id, kind, name, current, deleted_at, created_at FROM document;
DROP TABLE document;
ALTER TABLE document_new RENAME TO document;
CREATE UNIQUE INDEX document_name ON document (workspace_id, coalesce(project_id, 0), coalesce(record_id, 0), name);
CREATE TABLE chat_topic_new (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    archived      INTEGER NOT NULL DEFAULT 0,
    deleted_at    TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO chat_topic_new (id, workspace_id, title, archived, deleted_at, created_at, updated_at)
SELECT id, workspace_id, title, archived, deleted_at, created_at, updated_at FROM chat_topic;
DROP TABLE chat_topic;
ALTER TABLE chat_topic_new RENAME TO chat_topic;
CREATE INDEX chat_topic_workspace ON chat_topic (workspace_id, updated_at);
DROP TABLE card_edge;
DROP TABLE card;
COMMIT;
PRAGMA foreign_keys = ON;
