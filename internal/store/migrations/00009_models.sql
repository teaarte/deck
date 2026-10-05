-- +goose Up
-- A connection is a source of models: its API, address and key. A model is one model of a connection; one with an
-- agent is a provider of agent turns by its name. An assignment names the models of a kind of work, the first one
-- first, in the workspace, a project or a command.
CREATE TABLE connection (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    api           TEXT NOT NULL DEFAULT 'openai',
    url           TEXT NOT NULL,
    key_secret    TEXT NOT NULL DEFAULT '',
    local         INTEGER NOT NULL DEFAULT 0,
    daily_usd     REAL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (workspace_id, name)
);

CREATE TABLE model (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id   INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    connection_id  INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    model          TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT 'text',
    agent          TEXT NOT NULL DEFAULT '',
    input_usd      REAL,
    output_usd     REAL,
    call_usd       REAL,
    free           INTEGER NOT NULL DEFAULT 0,
    options        TEXT NOT NULL DEFAULT '{}',
    check_minutes  INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (workspace_id, name)
);
CREATE INDEX model_connection ON model (connection_id);

CREATE TABLE assignment (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER REFERENCES project(id) ON DELETE CASCADE,
    command       TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL,
    models        TEXT NOT NULL DEFAULT '[]'
);
CREATE UNIQUE INDEX assignment_place ON assignment (workspace_id, coalesce(project_id, 0), command, role);

-- The providers the owner added: one connection per address and key, a model with its agent each.
CREATE TEMP TABLE old_provider AS
SELECT workspace_id, substr(key, 10) AS name, CAST(json_extract(value, '$.url') AS TEXT) AS url,
    CAST(COALESCE(json_extract(value, '$.model'), '') AS TEXT) AS model,
    CAST(COALESCE(json_extract(value, '$.key_secret'), '') AS TEXT) AS key_secret,
    COALESCE(json_extract(value, '$.no_cloud'), 0) AS local,
    CAST(COALESCE(json_extract(value, '$.agent'), '') AS TEXT) AS agent,
    COALESCE(json_extract(value, '$.check_minutes'), 0) AS check_minutes
FROM setting WHERE substr(key, 1, 9) = 'provider:' AND json_valid(value) AND json_extract(value, '$.url') IS NOT NULL;
CREATE TEMP TABLE old_connection AS
SELECT workspace_id, url, key_secret, max(local) AS local, min(rowid) AS n,
    CASE WHEN lower(replace(replace(key_secret, '_', '-'), '.', '-')) GLOB '[a-z]*'
        AND lower(replace(replace(key_secret, '_', '-'), '.', '-')) NOT GLOB '*[^a-z0-9-]*'
        THEN lower(replace(replace(key_secret, '_', '-'), '.', '-')) ELSE 'local' END AS base
FROM old_provider GROUP BY workspace_id, url, key_secret;
INSERT INTO connection (workspace_id, name, api, url, key_secret, local)
SELECT c.workspace_id,
    CASE WHEN (SELECT count(*) FROM old_connection o WHERE o.workspace_id = c.workspace_id AND o.base = c.base AND o.n < c.n) = 0 THEN c.base
        WHEN EXISTS (SELECT 1 FROM old_connection o WHERE o.workspace_id = c.workspace_id AND o.base = c.base || '-' || c.n) THEN 'connection-' || c.n
        ELSE c.base || '-' || c.n END,
    'openai', c.url, c.key_secret, c.local
FROM old_connection c ORDER BY c.n;
INSERT INTO model (workspace_id, connection_id, name, model, kind, agent, check_minutes)
SELECT p.workspace_id, k.id, p.name, p.model, 'text', p.agent, p.check_minutes
FROM old_provider p JOIN connection k ON k.workspace_id = p.workspace_id AND k.url = p.url AND k.key_secret = p.key_secret;
DELETE FROM setting WHERE substr(key, 1, 9) = 'provider:' AND key IN (SELECT 'provider:' || name FROM old_provider);
DROP TABLE old_provider;
DROP TABLE old_connection;

-- Speech to text the owner set: a connection deck posts the recording to as it is, its model and the assignment.
INSERT INTO connection (workspace_id, name, api, url, key_secret)
SELECT s.workspace_id, CASE WHEN EXISTS (SELECT 1 FROM connection c WHERE c.workspace_id = s.workspace_id AND c.name = 'stt') THEN 'stt-voice' ELSE 'stt' END,
    'raw', CAST(json_extract(s.value, '$.url') AS TEXT), CAST(COALESCE(json_extract(s.value, '$.key_secret'), '') AS TEXT)
FROM setting s WHERE s.key = 'setting:stt' AND json_valid(s.value) AND json_extract(s.value, '$.url') IS NOT NULL;
INSERT INTO model (workspace_id, connection_id, name, model, kind, options)
SELECT s.workspace_id, k.id, CASE WHEN EXISTS (SELECT 1 FROM model m WHERE m.workspace_id = s.workspace_id AND m.name = 'stt') THEN 'stt-voice' ELSE 'stt' END,
    CAST(COALESCE(json_extract(s.value, '$.model'), '') AS TEXT), 'stt', json_object('language', COALESCE(json_extract(s.value, '$.language'), 'auto'))
FROM setting s JOIN connection k ON k.workspace_id = s.workspace_id AND k.api = 'raw' WHERE s.key = 'setting:stt';
INSERT INTO assignment (workspace_id, role, models) SELECT workspace_id, 'stt', json_array(name) FROM model WHERE kind = 'stt';
DELETE FROM setting WHERE key = 'setting:stt';

-- The provider the owner set for a command is the assignment of agent turns of that command.
INSERT INTO assignment (workspace_id, project_id, command, role, models)
SELECT j.workspace_id, c.project_id, c.command, 'agent', json_array(c.provider)
FROM command_provider c JOIN project j ON j.id = c.project_id;
DROP TABLE command_provider;
