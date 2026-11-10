-- +goose Up
-- Notes of a cause or of the same text join an unread one in Go (store.AddNote), not in triggers.
DROP TRIGGER IF EXISTS feed_cause;
DROP TRIGGER IF EXISTS feed_repeat;

-- +goose Down
SELECT 1;
