-- +goose Up
-- A project has no limit of models of its own: the daily limit of a connection is the only one.
ALTER TABLE project DROP COLUMN model_daily_usd;
