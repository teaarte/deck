-- +goose Up
-- The process that created a record, so the master may delete what its own processes wrote.
ALTER TABLE record ADD COLUMN process_id INTEGER REFERENCES process(id) ON DELETE SET NULL;
