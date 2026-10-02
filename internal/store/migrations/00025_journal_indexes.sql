-- +goose Up
CREATE INDEX tool_call_process ON tool_call (process_id);
CREATE INDEX process_project ON process (project_id);
CREATE INDEX process_open ON process (state) WHERE finished_at IS NULL;

-- +goose Down
DROP INDEX process_open;
DROP INDEX process_project;
DROP INDEX tool_call_process;
