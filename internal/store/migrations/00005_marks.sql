-- +goose Up
-- A mark keeps only cursors and throttles: the folder a project was imported from is a column of the project, the
-- notes deck tells the master are rows of the feed for it.
ALTER TABLE project ADD COLUMN imported_from TEXT NOT NULL DEFAULT '';
UPDATE project SET imported_from = COALESCE((SELECT m.value FROM mark m
    WHERE m.workspace_id = project.workspace_id AND m.name = 'imported_from:' || project.id), '');
ALTER TABLE feed ADD COLUMN for_master INTEGER NOT NULL DEFAULT 0;
INSERT INTO feed (workspace_id, project_id, kind, author, text, for_master)
SELECT m.workspace_id, j.id, 'note', 'deck', m.value, 1 FROM mark m
JOIN project j ON j.workspace_id = m.workspace_id AND j.builtin = 1
WHERE m.name = 'master_notes' AND m.value != '';
DELETE FROM mark WHERE name = 'master_notes' OR substr(name, 1, 14) = 'imported_from:';
