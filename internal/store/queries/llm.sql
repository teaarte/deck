-- name: LogModelCall :exec
-- A call of a process takes the project and command of the process.
INSERT INTO llm_call (workspace_id, project_id, process_id, command, connection_id, gen_ai_provider_name, gen_ai_request_model,
    gen_ai_response_model, gen_ai_usage_input_tokens, gen_ai_usage_output_tokens, gen_ai_usage_cache_read_input_tokens,
    gen_ai_usage_cache_creation_input_tokens, duration_ms, cost_usd, created_at)
VALUES (sqlc.arg(workspace_id),
    COALESCE((SELECT p.project_id FROM process p WHERE p.id = sqlc.narg(process_id)), CAST(sqlc.narg(project_id) AS INTEGER)), sqlc.narg(process_id),
    COALESCE((SELECT p.command FROM process p WHERE p.id = sqlc.narg(process_id)), ''),
    sqlc.narg(connection_id),
    sqlc.arg(provider), sqlc.arg(request_model), sqlc.arg(response_model), sqlc.arg(input_tokens), sqlc.arg(output_tokens),
    sqlc.arg(cache_read_tokens), sqlc.arg(cache_creation_tokens), sqlc.arg(duration_ms), sqlc.narg(cost_usd), sqlc.arg(created_at));


-- name: SpendByCommand :many
SELECT CAST(COALESCE(c.project_id, 0) AS INTEGER) AS project_id, c.command, c.gen_ai_provider_name AS provider, c.gen_ai_request_model AS model,
    CAST(count(*) AS INTEGER) AS calls,
    CAST(sum(c.gen_ai_usage_input_tokens) AS INTEGER) AS input_tokens,
    CAST(sum(c.gen_ai_usage_output_tokens) AS INTEGER) AS output_tokens,
    CAST(sum(c.gen_ai_usage_cache_read_input_tokens) AS INTEGER) AS cache_read_tokens,
    CAST(COALESCE(sum(c.cost_usd), 0) AS REAL) AS cost_usd,
    CAST(max(c.created_at) AS TEXT) AS last_at
FROM llm_call c
WHERE c.workspace_id = sqlc.arg(workspace_id)
GROUP BY c.project_id, c.command, c.gen_ai_provider_name, c.gen_ai_request_model
ORDER BY sum(c.gen_ai_usage_input_tokens + c.gen_ai_usage_output_tokens) DESC;

-- name: SpendSince :many
SELECT CAST(substr(c.created_at, 1, 16) AS TEXT) AS minute, c.gen_ai_provider_name AS provider, CAST(COALESCE(j.name, '') AS TEXT) AS project,
    CAST(count(*) AS INTEGER) AS calls,
    CAST(sum(c.gen_ai_usage_input_tokens) AS INTEGER) AS input_tokens,
    CAST(sum(c.gen_ai_usage_output_tokens) AS INTEGER) AS output_tokens,
    CAST(COALESCE(sum(c.cost_usd), 0) AS REAL) AS cost_usd
FROM llm_call c LEFT JOIN project j ON j.id = c.project_id
WHERE c.workspace_id = sqlc.arg(workspace_id) AND c.created_at >= sqlc.arg(since)
GROUP BY minute, provider, project;


-- name: ListProcessModelCalls :many
SELECT gen_ai_provider_name, cost_usd FROM llm_call WHERE process_id = ? ORDER BY id;
