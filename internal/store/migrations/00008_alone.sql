-- +goose Up
-- alone marks a trial of only its command: the commands that run after it do not start.
ALTER TABLE process ADD COLUMN alone INTEGER NOT NULL DEFAULT 0;
