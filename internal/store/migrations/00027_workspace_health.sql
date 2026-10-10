-- +goose Up
-- The health of a model of a connection is of its workspace, under model:, a copy in each workspace with a model of
-- that name; the row of the machine goes unless a provider of turns has that name. Pushes are of a workspace too.
INSERT OR IGNORE INTO health (subject, workspace_id, status, error, version, verdict, speed, updated_at)
SELECT 'model:' || m.name, m.workspace_id, h.status, h.error, h.version, h.verdict, h.speed, h.updated_at
FROM health h JOIN model m ON 'provider:' || m.name = h.subject AND m.agent = ''
WHERE h.workspace_id IS NULL;
DELETE FROM health WHERE workspace_id IS NULL AND subject GLOB 'provider:*'
    AND substr(subject, 10) NOT IN ('claude-code', 'codex')
    AND EXISTS (SELECT 1 FROM model m WHERE m.name = substr(health.subject, 10) AND m.agent = '')
    AND NOT EXISTS (SELECT 1 FROM model m WHERE m.name = substr(health.subject, 10) AND m.agent != '');
INSERT OR IGNORE INTO health (subject, workspace_id, status, error, version, verdict, speed, updated_at)
SELECT h.subject, w.id, h.status, h.error, h.version, h.verdict, h.speed, h.updated_at
FROM health h JOIN workspace w ON EXISTS (SELECT 1 FROM push_subscription s WHERE s.workspace_id = w.id)
WHERE h.workspace_id IS NULL AND h.subject = 'push';
DELETE FROM health WHERE workspace_id IS NULL AND subject = 'push';
