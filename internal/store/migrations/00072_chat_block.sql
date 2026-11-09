-- +goose Up
-- block is the one block of the catalog a message of the master shows; answer what the owner gave it, and when.
ALTER TABLE chat_message ADD COLUMN block TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_message ADD COLUMN answer TEXT NOT NULL DEFAULT '';
ALTER TABLE chat_message ADD COLUMN answered_at TEXT;

-- +goose Down
SELECT 1;
