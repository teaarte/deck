-- +goose Up
ALTER TABLE trigger_state ADD COLUMN params TEXT NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE trigger_state DROP COLUMN params;
