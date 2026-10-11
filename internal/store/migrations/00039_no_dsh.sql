-- +goose Up
-- deck runs dsh no more: its agent row goes, a model on it is red with what to do, and an open call of a model keeps its yes.
DELETE FROM health WHERE subject = 'agent:dsh';
DELETE FROM health WHERE subject IN (SELECT 'provider:' || name FROM model WHERE agent = 'dsh');
INSERT INTO health (subject, status, error)
SELECT 'provider:' || name, 'error', 'deck runs dsh no more; set the model again with deck.model_set and agent goose'
FROM model WHERE agent = 'dsh' GROUP BY name;
UPDATE ask SET payload = json_set(payload, '$.tool', 'deck.model_call')
WHERE state = 'open' AND json_extract(payload, '$.tool') = 'deck.model_request';
