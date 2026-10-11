-- +goose Up
-- A topic of the chat keeps the model its replies go on; empty is the model of the master's command.
ALTER TABLE chat_topic ADD COLUMN model TEXT NOT NULL DEFAULT '';
