-- +goose Up
-- The files of a message of the chat are stored file ids; a path an older message kept is dropped.
UPDATE chat_message SET context = json_set(context, '$.files',
    json((SELECT json_group_array(f.value) FROM json_each(chat_message.context, '$.files') f WHERE f.type = 'integer')))
WHERE json_valid(context) AND json_type(context, '$.files') = 'array'
  AND EXISTS (SELECT 1 FROM json_each(chat_message.context, '$.files') f WHERE f.type != 'integer');
