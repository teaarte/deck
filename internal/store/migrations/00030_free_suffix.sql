-- +goose Up
-- A model whose id ends in :free and that has no price costs nothing, as its connection's catalog says.
UPDATE model SET free = 1 WHERE model LIKE '%:free' AND input_usd IS NULL AND output_usd IS NULL AND call_usd IS NULL;
