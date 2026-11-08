-- name: SearchMemory :many
-- own narrows a confined actor to its project, the workspace and the skills of others; kinds is a list by commas.
SELECT CAST(f.rowid AS INTEGER) AS id, CAST(f.kind AS TEXT) AS kind,
    CAST(snippet(memory_fts, -1, '[', ']', '...', 16) AS TEXT) AS snippet, CAST(bm25(memory_fts, 0, 1) AS REAL) AS score,
    CAST(coalesce(d.project_id, p.project_id, 0) AS INTEGER) AS project_id, CAST(coalesce(d.name, p.command) AS TEXT) AS name,
    CAST(coalesce(d.current, 0) AS INTEGER) AS revision
FROM memory_fts f
LEFT JOIN document d ON d.id = f.rowid
LEFT JOIN process p ON p.id = -f.rowid
LEFT JOIN project pp ON pp.id = p.project_id
WHERE f.body MATCH sqlc.arg(query)
  AND coalesce(d.workspace_id, pp.workspace_id) = sqlc.arg(workspace_id)
  AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR coalesce(d.project_id, p.project_id) = sqlc.arg(project_id))
  AND (CAST(sqlc.arg(kinds) AS TEXT) = '' OR instr(',' || sqlc.arg(kinds) || ',', ',' || f.kind || ',') > 0)
  AND (CAST(sqlc.arg(own) AS INTEGER) = 0 OR coalesce(d.project_id, p.project_id, 0) IN (0, sqlc.arg(own)) OR f.kind = 'skill')
ORDER BY score, f.rowid
LIMIT sqlc.arg(lim) OFFSET sqlc.arg(skip);
