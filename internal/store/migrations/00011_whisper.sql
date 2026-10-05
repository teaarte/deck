-- +goose Up
-- Speech to text is a model like any other: a workspace without one gets the whisper.cpp server on this machine,
-- which detects the language, named whisper or deck-whisper when the owner has a whisper. Its health row is gone.
CREATE TEMP TABLE seed AS
SELECT w.id AS workspace_id,
    CASE WHEN EXISTS (SELECT 1 FROM connection c WHERE c.workspace_id = w.id AND c.name = 'whisper') THEN 'deck-whisper' ELSE 'whisper' END AS connection,
    CASE WHEN EXISTS (SELECT 1 FROM model m WHERE m.workspace_id = w.id AND m.name = 'whisper') THEN 'deck-whisper' ELSE 'whisper' END AS model
FROM workspace w
WHERE NOT EXISTS (SELECT 1 FROM assignment a WHERE a.workspace_id = w.id AND a.project_id IS NULL AND a.command = '' AND a.role = 'stt');
INSERT OR IGNORE INTO connection (workspace_id, name, api, url, local)
SELECT workspace_id, connection, 'raw', 'http://127.0.0.1:8178/inference', 1 FROM seed;
INSERT OR IGNORE INTO model (workspace_id, connection_id, name, model, kind, free, options)
SELECT s.workspace_id, c.id, s.model, 'whisper', 'stt', 1, '{"language": "auto"}' FROM seed s
JOIN connection c ON c.workspace_id = s.workspace_id AND c.name = s.connection AND c.api = 'raw';
INSERT INTO assignment (workspace_id, command, role, models)
SELECT s.workspace_id, '', 'stt', json_array(s.model) FROM seed s
WHERE EXISTS (SELECT 1 FROM model m WHERE m.workspace_id = s.workspace_id AND m.name = s.model AND m.kind = 'stt');
DROP TABLE seed;
DELETE FROM health WHERE subject = 'stt';
