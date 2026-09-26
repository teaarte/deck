-- +goose Up
ALTER TABLE tool_call ADD COLUMN external_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tool_call ADD COLUMN error TEXT NOT NULL DEFAULT '';
ALTER TABLE tool_call ADD COLUMN ask_id INTEGER;
CREATE INDEX tool_call_outcome ON tool_call (outcome);
ALTER TABLE ask ADD COLUMN result TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE ask DROP COLUMN result;
DROP INDEX tool_call_outcome;
ALTER TABLE tool_call DROP COLUMN ask_id;
ALTER TABLE tool_call DROP COLUMN error;
ALTER TABLE tool_call DROP COLUMN external_id;
