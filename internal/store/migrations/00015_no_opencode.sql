-- +goose Up
-- deck runs OpenCode no more: its agent row goes, and a model on it is red with what to do until the owner sets it again.
DELETE FROM health WHERE subject = 'agent:opencode';
DELETE FROM health WHERE subject IN (SELECT 'provider:' || name FROM model WHERE agent = 'opencode');
INSERT INTO health (subject, status, error)
SELECT 'provider:' || name, 'error', 'deck runs OpenCode no more; set the model again with deck.model_set and agent goose or dsh'
FROM model WHERE agent = 'opencode' GROUP BY name;
