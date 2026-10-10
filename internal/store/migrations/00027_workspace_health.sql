-- +goose Up
-- The health of a model of a connection is of its workspace, under model:; pushes are of a workspace too.
UPDATE health SET subject = 'model:' || substr(subject, 10),
    workspace_id = (SELECT m.workspace_id FROM model m WHERE m.name = substr(health.subject, 10) AND m.agent = '' ORDER BY m.id LIMIT 1)
WHERE workspace_id IS NULL AND subject GLOB 'provider:*'
    AND EXISTS (SELECT 1 FROM model m WHERE m.name = substr(health.subject, 10) AND m.agent = '');
DELETE FROM health WHERE workspace_id IS NULL AND subject = 'push';
