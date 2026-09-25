-- +goose Up
CREATE TABLE workspace (
    id    INTEGER PRIMARY KEY,
    name  TEXT NOT NULL
);
INSERT INTO workspace (id, name) VALUES (1, 'default');

ALTER TABLE project ADD COLUMN workspace_id INTEGER NOT NULL DEFAULT 1;
ALTER TABLE project ADD COLUMN status TEXT NOT NULL DEFAULT 'enabled';

-- project_id 0 is a workspace document; owner_kind/owner_id bind a document to a record or card.
CREATE TABLE document (
    id            INTEGER PRIMARY KEY,
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

CREATE TABLE document_revision (
    id           INTEGER PRIMARY KEY,
    document_id  INTEGER NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    n            INTEGER NOT NULL,
    text         TEXT NOT NULL,
    author       TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (document_id, n)
);

INSERT INTO document (project_id, kind, name, current) SELECT id, 'manifest', 'manifest.json', 1 FROM project;
INSERT INTO document_revision (document_id, n, text, author, reason)
SELECT d.id, 1, p.manifest, 'owner', 'migrated' FROM document d JOIN project p ON p.id = d.project_id WHERE d.kind = 'manifest';
ALTER TABLE project DROP COLUMN manifest;

-- +goose Down
ALTER TABLE project ADD COLUMN manifest TEXT NOT NULL DEFAULT '{}';
UPDATE project SET manifest = (
    SELECT r.text FROM document d JOIN document_revision r ON r.document_id = d.id AND r.n = d.current
    WHERE d.project_id = project.id AND d.kind = 'manifest');
DROP TABLE document_revision;
DROP TABLE document;
ALTER TABLE project DROP COLUMN status;
ALTER TABLE project DROP COLUMN workspace_id;
DROP TABLE workspace;
