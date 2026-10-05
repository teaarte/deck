-- +goose Up
-- Speech to text is a model like any other: a workspace without one gets the whisper.cpp server on this machine,
-- which detects the language. Its own health row is gone with its client.
INSERT OR IGNORE INTO connection (workspace_id, name, api, url, local)
SELECT w.id, 'whisper', 'raw', 'http://127.0.0.1:8178/inference', 1 FROM workspace w
WHERE NOT EXISTS (SELECT 1 FROM assignment a WHERE a.workspace_id = w.id AND a.project_id IS NULL AND a.command = '' AND a.role = 'stt');
INSERT OR IGNORE INTO model (workspace_id, connection_id, name, model, kind, free, options)
SELECT c.workspace_id, c.id, 'whisper', 'whisper', 'stt', 1, '{"language": "auto"}' FROM connection c
WHERE c.name = 'whisper' AND c.api = 'raw'
  AND NOT EXISTS (SELECT 1 FROM assignment a WHERE a.workspace_id = c.workspace_id AND a.project_id IS NULL AND a.command = '' AND a.role = 'stt');
INSERT INTO assignment (workspace_id, command, role, models)
SELECT m.workspace_id, '', 'stt', '["whisper"]' FROM model m
WHERE m.name = 'whisper' AND m.kind = 'stt'
  AND NOT EXISTS (SELECT 1 FROM assignment a WHERE a.workspace_id = m.workspace_id AND a.project_id IS NULL AND a.command = '' AND a.role = 'stt');
DELETE FROM health WHERE subject = 'stt';
