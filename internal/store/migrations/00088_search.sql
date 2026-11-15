-- +goose Up
-- SearXNG beside deck in compose.yaml is the search of every workspace that has none: its connection, its model and
-- the assignment of search, each only where the owner set up no search and no connection or model of that name.
CREATE TEMP TABLE searchless AS
SELECT w.id FROM workspace w
WHERE NOT EXISTS (SELECT 1 FROM assignment a WHERE a.workspace_id = w.id AND a.project_id IS NULL AND a.command = '' AND a.role = 'search')
  AND NOT EXISTS (SELECT 1 FROM connection c WHERE c.workspace_id = w.id AND c.name = 'searxng')
  AND NOT EXISTS (SELECT 1 FROM model m WHERE m.workspace_id = w.id AND m.name = 'searxng');
INSERT INTO connection (workspace_id, name, api, url, local) SELECT id, 'searxng', 'raw', 'http://searxng:8080', 1 FROM searchless;
INSERT INTO model (workspace_id, connection_id, name, model, kind, free)
SELECT s.id, c.id, 'searxng', 'searxng', 'search', 1 FROM searchless s JOIN connection c ON c.workspace_id = s.id AND c.name = 'searxng';
INSERT INTO assignment (workspace_id, command, role, models) SELECT id, '', 'search', '["searxng"]' FROM searchless;
DROP TABLE searchless;

-- +goose Down
-- The rows stay: the owner may have changed them since; going back is the snapshot of the update.
SELECT 1;
