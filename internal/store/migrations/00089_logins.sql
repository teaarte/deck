-- +goose Up
-- agent is the CLI a connection of api cli signs in to: a login of its own, the built-in ones of their own name.
ALTER TABLE connection ADD COLUMN agent TEXT NOT NULL DEFAULT '';
UPDATE connection SET agent = name WHERE api = 'cli';

-- +goose Down
ALTER TABLE connection DROP COLUMN agent;
