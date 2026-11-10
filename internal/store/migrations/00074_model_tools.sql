-- +goose Up
-- The most tools one request of a model takes, as its API said or the owner set; 0 is not known.
ALTER TABLE model ADD COLUMN tools INTEGER NOT NULL DEFAULT 0;
