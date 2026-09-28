-- name: LogLLMCall :exec
INSERT INTO llm_call (process_id, gen_ai_provider_name, gen_ai_request_model, gen_ai_response_model, gen_ai_usage_input_tokens,
    gen_ai_usage_output_tokens, gen_ai_usage_cache_read_input_tokens, gen_ai_usage_cache_creation_input_tokens, duration_ms, cost_usd,
    created_at)
VALUES (sqlc.arg(process_id), sqlc.arg(provider), sqlc.arg(request_model), sqlc.arg(response_model), sqlc.arg(input_tokens),
    sqlc.arg(output_tokens), sqlc.arg(cache_read_tokens), sqlc.arg(cache_creation_tokens), sqlc.arg(duration_ms), sqlc.narg(cost_usd),
    sqlc.arg(created_at));


-- name: ListLLMCalls :many
SELECT c.* FROM llm_call c JOIN process p ON p.id = c.process_id JOIN project j ON j.id = p.project_id
WHERE j.workspace_id = sqlc.arg(workspace_id) AND (sqlc.arg(process_id) = 0 OR c.process_id = sqlc.arg(process_id))
  AND (sqlc.arg(project_id) = 0 OR p.project_id = sqlc.arg(project_id))
ORDER BY c.id DESC LIMIT sqlc.arg(lim);


-- name: SpendByCommand :many
SELECT p.project_id, p.command, c.gen_ai_provider_name AS provider, c.gen_ai_request_model AS model,
    CAST(count(*) AS INTEGER) AS calls,
    CAST(sum(c.gen_ai_usage_input_tokens) AS INTEGER) AS input_tokens,
    CAST(sum(c.gen_ai_usage_output_tokens) AS INTEGER) AS output_tokens,
    CAST(sum(c.gen_ai_usage_cache_read_input_tokens) AS INTEGER) AS cache_read_tokens,
    CAST(COALESCE(sum(c.cost_usd), 0) AS REAL) AS cost_usd,
    CAST(max(c.created_at) AS TEXT) AS last_at
FROM llm_call c JOIN process p ON p.id = c.process_id JOIN project j ON j.id = p.project_id
WHERE j.workspace_id = sqlc.arg(workspace_id)
GROUP BY p.project_id, p.command, c.gen_ai_provider_name, c.gen_ai_request_model
ORDER BY sum(c.gen_ai_usage_input_tokens + c.gen_ai_usage_output_tokens) DESC;
