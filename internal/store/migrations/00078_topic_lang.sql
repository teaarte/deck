-- +goose Up
-- The language of a topic, as the model of its reply marked it; empty follows the settings.
ALTER TABLE chat_topic ADD COLUMN lang TEXT NOT NULL DEFAULT '';
