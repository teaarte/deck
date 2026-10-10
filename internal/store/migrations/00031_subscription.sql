-- +goose Up
-- A call on a subscription keeps what it would cost as an estimate, out of the money spent; a model of the settings
-- may be paid by a subscription.
ALTER TABLE llm_call ADD COLUMN subscription INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model ADD COLUMN subscription INTEGER NOT NULL DEFAULT 0;
UPDATE llm_call SET subscription = 1 WHERE connection_id IS NULL AND gen_ai_provider_name IN ('claude-code', 'codex');
