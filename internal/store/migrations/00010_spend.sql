-- +goose Up
-- A model call keeps the connection it went through, so a day of a connection sums to its spend; a project may
-- have a limit of its own.
ALTER TABLE llm_call ADD COLUMN connection_id INTEGER REFERENCES connection(id) ON DELETE SET NULL;
UPDATE llm_call SET connection_id = (SELECT m.connection_id FROM model m
    WHERE m.workspace_id = llm_call.workspace_id AND m.name = llm_call.gen_ai_provider_name);
CREATE INDEX llm_call_connection ON llm_call (connection_id, created_at);
ALTER TABLE project ADD COLUMN model_daily_usd REAL;
-- The old sandbox is gone with its setting.
DELETE FROM setting WHERE key = 'setting:sandbox';
