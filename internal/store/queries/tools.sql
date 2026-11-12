-- name: UpsertMCPTool :exec
INSERT INTO mcp_tool (project_id, server, spec, name, title, description, input, fingerprint)
VALUES (sqlc.arg(project_id), sqlc.arg(server), sqlc.arg(spec), sqlc.arg(name), sqlc.arg(title), sqlc.arg(description),
    sqlc.arg(input), sqlc.arg(fingerprint))
ON CONFLICT (project_id, server, name) DO UPDATE SET title = excluded.title, description = excluded.description,
    input = excluded.input, fingerprint = excluded.fingerprint, listed = 1, spec = excluded.spec;


-- name: ListMCPTools :many
SELECT * FROM mcp_tool WHERE project_id = ? ORDER BY server, name;


-- name: UnlistMCPToolsExcept :exec
UPDATE mcp_tool SET listed = 0 WHERE project_id = sqlc.arg(project_id) AND server = sqlc.arg(server)
  AND name NOT IN (SELECT value FROM json_each(CAST(sqlc.arg(keep) AS TEXT)));

