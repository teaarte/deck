-- +goose Up
-- deleted_at hides a document; its revisions stay, and the owner can bring it back.
ALTER TABLE document ADD COLUMN deleted_at TEXT;

-- +goose Down
ALTER TABLE document DROP COLUMN deleted_at;
