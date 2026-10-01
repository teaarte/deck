-- +goose Up
-- deleted_at hides a document; its revisions stay, and the owner can bring it back.
ALTER TABLE document ADD COLUMN deleted_at TEXT;
-- memory is the sign of the memory a chat session was last given.
ALTER TABLE chat_session ADD COLUMN memory TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chat_session DROP COLUMN memory;
ALTER TABLE document DROP COLUMN deleted_at;
