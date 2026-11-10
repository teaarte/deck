-- +goose Up
-- The episodes of a period read processes by when they finished and the owner's messages by when he wrote them.
CREATE INDEX IF NOT EXISTS process_episode ON process (finished_at) WHERE trial = 0;
CREATE INDEX IF NOT EXISTS chat_message_owner ON chat_message (created_at) WHERE role = 'owner';
