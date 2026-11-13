-- +goose Up
-- What a turn knows and from where: the untrusted text it read and whether it holds something of value; whether the data
-- of a module holds untrusted text; whether the owner vouches for what an automation token writes.
ALTER TABLE process ADD COLUMN read_untrusted TEXT NOT NULL DEFAULT '';
ALTER TABLE process ADD COLUMN holds_valued INTEGER NOT NULL DEFAULT 0;
ALTER TABLE project ADD COLUMN untrusted TEXT NOT NULL DEFAULT '';
ALTER TABLE automation_token ADD COLUMN trusted INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE automation_token DROP COLUMN trusted;
ALTER TABLE project DROP COLUMN untrusted;
ALTER TABLE process DROP COLUMN holds_valued;
ALTER TABLE process DROP COLUMN read_untrusted;
