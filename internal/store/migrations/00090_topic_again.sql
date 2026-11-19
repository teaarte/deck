-- +goose Up
-- What the next reply of a topic goes on with when the owner answered a broken one again: its provider, the note of
-- what the broken one did and that reply's process.
ALTER TABLE chat_topic ADD COLUMN again_provider TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_topic ADD COLUMN again_note TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_topic ADD COLUMN again_follows INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE chat_topic DROP COLUMN again_follows;
ALTER TABLE chat_topic DROP COLUMN again_note;
ALTER TABLE chat_topic DROP COLUMN again_provider;
