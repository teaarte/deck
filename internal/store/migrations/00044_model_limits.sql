-- +goose Up
-- The limits of a model: tokens a minute and tokens of one request; 0 is not known.
ALTER TABLE model ADD COLUMN tpm INTEGER NOT NULL DEFAULT 0;
ALTER TABLE model ADD COLUMN context INTEGER NOT NULL DEFAULT 0;
