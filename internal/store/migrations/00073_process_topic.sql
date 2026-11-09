-- +goose Up
-- The processes of a topic are found by its id in their input, on each read of the chat.
CREATE INDEX IF NOT EXISTS process_topic ON process (project_id, json_extract(input, '$.topic'));

-- +goose Down
SELECT 1;
