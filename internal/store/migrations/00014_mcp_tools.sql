-- +goose Up
-- Tools an MCP server listed (listed = 1 while it still lists them); approved is the fingerprint the rights
-- were given to: set when deck first sees the tool under this server spec, or when the owner allows it again.
CREATE TABLE mcp_tool (
    project_id   INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    server       TEXT NOT NULL,
    spec         TEXT NOT NULL,
    name         TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    input        TEXT NOT NULL DEFAULT '{}',
    fingerprint  TEXT NOT NULL,
    approved     TEXT NOT NULL,
    listed       INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (project_id, server, name)
);

-- +goose Down
DROP TABLE mcp_tool;
