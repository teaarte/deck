-- +goose Up
-- A failed process the owner closed in the cockpit.
ALTER TABLE process ADD COLUMN dismissed INTEGER NOT NULL DEFAULT 0;
