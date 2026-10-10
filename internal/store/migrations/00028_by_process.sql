-- +goose Up
-- The process that created a record or made a change of the feed: the master deletes what its own processes wrote,
-- and a turn the provider broke off lists what it left, with its undo. Ids of processes are never reused.
ALTER TABLE record ADD COLUMN process_id INTEGER;
ALTER TABLE feed ADD COLUMN process_id INTEGER;
