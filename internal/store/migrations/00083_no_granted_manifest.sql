-- +goose Up
-- The owner's yes lives in right_grant: no granted revision of a manifest, no approved print of an MCP tool.
ALTER TABLE project DROP COLUMN granted_revision;
ALTER TABLE mcp_tool DROP COLUMN approved;

-- +goose Down
ALTER TABLE project ADD COLUMN granted_revision INTEGER;
ALTER TABLE mcp_tool ADD COLUMN approved TEXT NOT NULL DEFAULT '';
