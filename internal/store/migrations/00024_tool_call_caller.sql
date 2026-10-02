-- +goose Up
ALTER TABLE tool_call ADD COLUMN caller TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE tool_call DROP COLUMN caller;
