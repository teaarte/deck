-- +goose Up
-- One question about a call: the journal keeps the right a call needs, not a class; a confirmation of a tool of an
-- agent's provider is a call its turn makes, a login of a provider a sign-in; deck notes when the owner saw a request.
ALTER TABLE tool_call RENAME COLUMN class TO need;
UPDATE tool_call SET need = '' WHERE need = 'internal';
UPDATE ask SET kind = 'call', state = CASE state WHEN 'used' THEN 'answered' ELSE state END,
    payload = json_object('call', json_object('command', coalesce(json_extract(payload, '$.tool'), ''),
        'input', json(coalesce(json_extract(payload, '$.input'), '{}'))),
        'caller', json_object('kind', 'agent', 'workspace', workspace_id, 'project', coalesce(project_id, 0), 'process', coalesce(process_id, 0)),
        'as', 'turn')
WHERE kind = 'confirm';
UPDATE ask SET kind = 'sign_in', payload = json_object('provider', json('true'), 'answers', json(coalesce(json_extract(payload, '$.answers'), '["retry","new_session"]')))
WHERE kind = 'login';
ALTER TABLE ask ADD COLUMN shown_at TEXT;

-- +goose Down
ALTER TABLE ask DROP COLUMN shown_at;
UPDATE ask SET kind = 'login', payload = json_object('answers', json(coalesce(json_extract(payload, '$.answers'), '["retry","new_session"]')))
WHERE kind = 'sign_in' AND json_extract(payload, '$.provider') IS NOT NULL;
UPDATE ask SET kind = 'confirm', payload = json_object('tool', json_extract(payload, '$.call.command'), 'input', json(json_extract(payload, '$.call.input')))
WHERE kind = 'call' AND json_extract(payload, '$.as') = 'turn';
UPDATE tool_call SET need = 'internal' WHERE need = '';
ALTER TABLE tool_call RENAME COLUMN need TO class;
