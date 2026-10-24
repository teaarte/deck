-- +goose Up
-- A model of a connection with the same API name, kind and options is one model: a later twin goes into the first,
-- which takes its agent, assignments and chat topics.
CREATE TEMP TABLE twin AS
SELECT d.id, d.name, d.agent, d.check_minutes, k.id AS keep_id, k.name AS keep
FROM model d JOIN model k ON k.id = (SELECT min(m.id) FROM model m
    WHERE m.connection_id = d.connection_id AND m.model = d.model AND m.kind = d.kind AND json(m.options) = json(d.options))
WHERE d.id != k.id;

UPDATE model SET
    agent = (SELECT t.agent FROM twin t WHERE t.keep_id = model.id AND t.agent != '' ORDER BY t.id LIMIT 1),
    check_minutes = (SELECT t.check_minutes FROM twin t WHERE t.keep_id = model.id AND t.agent != '' ORDER BY t.id LIMIT 1)
WHERE agent = '' AND id IN (SELECT keep_id FROM twin WHERE agent != '');

UPDATE assignment SET models = (
    SELECT json_group_array(v) FROM (
        SELECT coalesce(t.keep, j.value) AS v, min(j.key) AS k FROM json_each(assignment.models) j
        LEFT JOIN twin t ON t.name = j.value GROUP BY v ORDER BY k))
WHERE EXISTS (SELECT 1 FROM json_each(assignment.models) j JOIN twin t ON t.name = j.value);

UPDATE chat_topic SET model = (SELECT t.keep FROM twin t WHERE t.name = chat_topic.model)
WHERE model IN (SELECT name FROM twin);

UPDATE process SET provider = (SELECT t.keep FROM twin t WHERE t.name = process.provider)
WHERE provider IN (SELECT name FROM twin);

UPDATE OR IGNORE chat_session SET provider = (SELECT t.keep FROM twin t WHERE t.name = chat_session.provider)
WHERE provider IN (SELECT name FROM twin);

DELETE FROM chat_session WHERE provider IN (SELECT name FROM twin);

DELETE FROM health WHERE subject IN (SELECT 'model:' || name FROM twin UNION SELECT 'provider:' || name FROM twin);

DELETE FROM file WHERE owner_kind = 'model' AND owner_id IN (SELECT id FROM twin);

DELETE FROM model WHERE id IN (SELECT id FROM twin);

DROP TABLE twin;
