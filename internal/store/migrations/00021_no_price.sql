-- +goose Up
-- deck keeps no prices of models: a call costs what its provider says, else nothing is kept.
ALTER TABLE model DROP COLUMN input_usd;
ALTER TABLE model DROP COLUMN output_usd;
ALTER TABLE model DROP COLUMN call_usd;
