-- +goose Up
-- A warning of deck is a note of the feed the owner marks read, not an ask: the open ones move there.
ALTER TABLE feed ADD COLUMN read_at TEXT;
INSERT INTO feed (workspace_id, project_id, kind, author, text)
SELECT a.workspace_id, COALESCE(a.project_id, (SELECT j.id FROM project j WHERE j.workspace_id = a.workspace_id AND j.builtin = 1)),
    'note', 'deck', a.text
FROM ask a WHERE a.kind = 'warning' AND a.state = 'open'
  AND COALESCE(a.project_id, (SELECT j.id FROM project j WHERE j.workspace_id = a.workspace_id AND j.builtin = 1)) IS NOT NULL;
UPDATE ask SET state = 'answered', answer = 'ok', answered_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
WHERE kind = 'warning' AND state = 'open';
