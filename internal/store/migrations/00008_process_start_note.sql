-- +goose Up
ALTER TABLE process ADD COLUMN start_note TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE process DROP COLUMN start_note;
