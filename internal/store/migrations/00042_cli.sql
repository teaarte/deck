-- +goose Up
-- The agent CLIs on the owner's subscription are connections deck keeps itself, without an address or a key; their
-- models run agent turns in the CLI.
INSERT OR IGNORE INTO connection (workspace_id, name, api, url) SELECT id, 'claude-code', 'cli', '' FROM workspace;
INSERT OR IGNORE INTO connection (workspace_id, name, api, url) SELECT id, 'codex', 'cli', '' FROM workspace;

