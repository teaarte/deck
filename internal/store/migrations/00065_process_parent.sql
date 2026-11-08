-- +goose Up
-- The work a process started is found by its parent, such as the tokens a chat topic spent.
CREATE INDEX IF NOT EXISTS process_parent ON process (parent_id) WHERE parent_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS process_parent;
