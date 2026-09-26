-- name: UpsertMCPTool :exec
INSERT INTO mcp_tool (project_id, server, spec, name, title, description, input, fingerprint, approved)
VALUES (sqlc.arg(project_id), sqlc.arg(server), sqlc.arg(spec), sqlc.arg(name), sqlc.arg(title), sqlc.arg(description),
    sqlc.arg(input), sqlc.arg(fingerprint), sqlc.arg(fingerprint))
ON CONFLICT (project_id, server, name) DO UPDATE SET title = excluded.title, description = excluded.description,
    input = excluded.input, fingerprint = excluded.fingerprint,
    approved = CASE WHEN mcp_tool.spec = excluded.spec THEN mcp_tool.approved ELSE excluded.fingerprint END,
    spec = excluded.spec;


-- name: ListMCPTools :many
SELECT * FROM mcp_tool WHERE project_id = ? ORDER BY server, name;


-- name: DeleteMCPToolsExcept :exec
DELETE FROM mcp_tool WHERE project_id = sqlc.arg(project_id) AND server = sqlc.arg(server)
  AND name NOT IN (SELECT value FROM json_each(sqlc.arg(keep)));


-- name: ApproveMCPTool :exec
UPDATE mcp_tool SET approved = fingerprint WHERE project_id = ? AND server = ? AND name = ?;
