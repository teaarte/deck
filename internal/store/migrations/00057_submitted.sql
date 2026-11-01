-- +goose Up
-- submitted_at is when an agent handed in a result by its schema, which the process keeps from then on.
ALTER TABLE process ADD COLUMN submitted_at TEXT;

-- +goose Down
SELECT 1;
