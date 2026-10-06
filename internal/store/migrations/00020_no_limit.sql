-- +goose Up
-- deck keeps no limits of money: the owner sets them at the provider.
ALTER TABLE connection DROP COLUMN daily_usd;
