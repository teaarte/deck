-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;
BEGIN;
ALTER TABLE project ADD COLUMN paused INTEGER NOT NULL DEFAULT 0;

-- Deadlines are unix milliseconds of the deck clock; deadline_ms is NULL while the ceiling is paused and
-- ceiling_ms then holds what is left of it.
ALTER TABLE process ADD COLUMN parent_id INTEGER;
ALTER TABLE process ADD COLUMN depth INTEGER NOT NULL DEFAULT 1;
ALTER TABLE process ADD COLUMN trigger_id INTEGER;
ALTER TABLE process ADD COLUMN retry_key TEXT NOT NULL DEFAULT '';
ALTER TABLE process ADD COLUMN pgid INTEGER NOT NULL DEFAULT 0;
ALTER TABLE process ADD COLUMN ceiling_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE process ADD COLUMN deadline_ms INTEGER;
ALTER TABLE process ADD COLUMN output_ms INTEGER NOT NULL DEFAULT 0;
ALTER TABLE process ADD COLUMN silent_since_ms INTEGER;
CREATE INDEX process_retry ON process (project_id, command, retry_key) WHERE retry_key != '';
-- Processes still going get the default ceiling of a command agent, open confirmations the default day.
UPDATE process SET ceiling_ms = 3600000 WHERE state IN ('running', 'waiting_answer', 'needs_login');

ALTER TABLE ask ADD COLUMN expires_at TEXT;
UPDATE ask SET expires_at = strftime('%Y-%m-%dT%H:%M:%fZ', created_at, '+1 day') WHERE kind = 'confirm' AND state = 'open';

-- kind is cron, lane, after or once; spec is the cron expression, lane or command; once rows are run-later.
CREATE TABLE trigger_new (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command          TEXT NOT NULL,
    kind             TEXT NOT NULL,
    spec             TEXT NOT NULL DEFAULT '',
    params           TEXT NOT NULL DEFAULT '{}',
    next_run_at      TEXT,
    paused           INTEGER NOT NULL DEFAULT 0,
    skip_missed      INTEGER NOT NULL DEFAULT 0,
    last_process_id  INTEGER
);
INSERT INTO trigger_new (project_id, command, kind, spec, params, next_run_at)
SELECT project_id, command, 'cron', cron, params, next_run_at FROM trigger_state;
DROP TABLE trigger_state;
ALTER TABLE trigger_new RENAME TO trigger_state;
CREATE UNIQUE INDEX trigger_identity ON trigger_state (project_id, command, kind, spec) WHERE kind != 'once';
COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
SELECT 1;
