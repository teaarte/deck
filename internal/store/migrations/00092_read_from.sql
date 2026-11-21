-- +goose Up
-- Where the untrusted text of a process or of the data of a module came from, as data: its first source and the data
-- of a module it last passed through (JSON); empty for a mark written before it.
ALTER TABLE process ADD COLUMN read_from TEXT NOT NULL DEFAULT '';
ALTER TABLE project ADD COLUMN read_from TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE project DROP COLUMN read_from;
ALTER TABLE process DROP COLUMN read_from;
