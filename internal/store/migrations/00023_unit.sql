-- +goose Up
-- What a price per million of a model counts: tokens, characters sent, or seconds the API reported.
ALTER TABLE model ADD COLUMN unit TEXT NOT NULL DEFAULT 'token';
