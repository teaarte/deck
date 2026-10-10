-- +goose Up
-- When a process was last sent back to work; its duration and its week in the cockpit count from then.
ALTER TABLE process ADD COLUMN reopened_at TEXT;
