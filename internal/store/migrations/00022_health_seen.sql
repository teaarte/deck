-- +goose Up
-- When the owner read a failing check; it counts again once its status or error changes.
ALTER TABLE health ADD COLUMN seen_at TEXT;
