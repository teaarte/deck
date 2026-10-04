-- +goose Up
-- A model call keeps its workspace, project and command, so the spend outlives its process and project.
CREATE TABLE llm_call_new (
    id                                        INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id                              INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id                                INTEGER REFERENCES project(id) ON DELETE SET NULL,
    process_id                                INTEGER REFERENCES process(id) ON DELETE SET NULL,
    command                                   TEXT NOT NULL DEFAULT '',
    gen_ai_provider_name                      TEXT NOT NULL,
    gen_ai_request_model                      TEXT NOT NULL DEFAULT '',
    gen_ai_response_model                     TEXT NOT NULL DEFAULT '',
    gen_ai_usage_input_tokens                 INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_output_tokens                INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_read_input_tokens      INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_creation_input_tokens  INTEGER NOT NULL DEFAULT 0,
    duration_ms                               INTEGER NOT NULL DEFAULT 0,
    cost_usd                                  REAL,
    created_at                                TEXT NOT NULL
);
INSERT INTO llm_call_new (id, workspace_id, project_id, process_id, command, gen_ai_provider_name, gen_ai_request_model,
    gen_ai_response_model, gen_ai_usage_input_tokens, gen_ai_usage_output_tokens, gen_ai_usage_cache_read_input_tokens,
    gen_ai_usage_cache_creation_input_tokens, duration_ms, cost_usd, created_at)
SELECT c.id, j.workspace_id, p.project_id, c.process_id, p.command, c.gen_ai_provider_name, c.gen_ai_request_model,
    c.gen_ai_response_model, c.gen_ai_usage_input_tokens, c.gen_ai_usage_output_tokens, c.gen_ai_usage_cache_read_input_tokens,
    c.gen_ai_usage_cache_creation_input_tokens, c.duration_ms, c.cost_usd, c.created_at
FROM llm_call c JOIN process p ON p.id = c.process_id JOIN project j ON j.id = p.project_id;
DROP TABLE llm_call;
ALTER TABLE llm_call_new RENAME TO llm_call;
CREATE INDEX llm_call_process ON llm_call (process_id);
CREATE INDEX llm_call_workspace ON llm_call (workspace_id, created_at);
