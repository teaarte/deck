-- +goose Up
ALTER TABLE tool_call ADD COLUMN workspace_id INTEGER NOT NULL DEFAULT 0;
UPDATE tool_call SET workspace_id = COALESCE((SELECT workspace_id FROM project WHERE project.id = tool_call.project_id), 0);
CREATE INDEX tool_call_workspace ON tool_call (workspace_id, id);

-- +goose Down
DROP INDEX tool_call_workspace;
ALTER TABLE tool_call DROP COLUMN workspace_id;
