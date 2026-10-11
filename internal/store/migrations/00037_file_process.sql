-- +goose Up
-- The process that made a file: the master deletes a file its own process gave a record.
ALTER TABLE file ADD COLUMN process_id INTEGER;
