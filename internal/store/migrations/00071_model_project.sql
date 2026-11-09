-- +goose Up
-- project_id is the module whose call added a model, which removes it without the owner while it is free and unassigned.
ALTER TABLE model ADD COLUMN project_id INTEGER;

-- +goose Down
SELECT 1;
