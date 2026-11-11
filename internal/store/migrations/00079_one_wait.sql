-- +goose Up
-- A process waits in one state: why (wait_reason), on what (wait_on), until when (wait_until_ms) and what its
-- silence comes to (wait_silence).
ALTER TABLE process RENAME COLUMN pause_until_ms TO wait_until_ms;
ALTER TABLE process ADD COLUMN wait_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE process ADD COLUMN wait_on TEXT NOT NULL DEFAULT '';
ALTER TABLE process ADD COLUMN wait_silence TEXT NOT NULL DEFAULT '';
UPDATE process SET wait_reason = CASE state
    WHEN 'waiting_answer' THEN 'answer' WHEN 'needs_login' THEN 'sign_in' WHEN 'rate_limited' THEN 'limit'
    WHEN 'waiting_provider' THEN 'provider' WHEN 'waiting_slot' THEN 'slot' WHEN 'waiting_folder' THEN 'folder'
    ELSE 'run' END,
  wait_silence = CASE state
    WHEN 'waiting_answer' THEN 'go_on' WHEN 'needs_login' THEN 'fail:model.login' WHEN 'rate_limited' THEN 'retry'
    WHEN 'waiting_provider' THEN 'fail:model.unavailable' ELSE '' END,
  wait_on = CASE WHEN state IN ('rate_limited', 'waiting_provider') THEN provider ELSE '' END,
  wait_until_ms = CASE WHEN state = 'rate_limited' THEN wait_until_ms ELSE NULL END,
  state = 'waiting'
WHERE state IN ('waiting_answer', 'needs_login', 'rate_limited', 'waiting_provider', 'waiting_slot', 'waiting_folder', 'waiting_run');
UPDATE chat_message SET context = replace(context, '"state":"waiting_answer"', '"state":"waiting"')
WHERE context LIKE '%"state":"waiting_answer"%';
-- The questions and sign-ins left open get the term the new ones get, a day and a week from when they were asked.
UPDATE ask SET expires_at = strftime('%Y-%m-%dT%H:%M:%fZ', created_at, '+1 day')
WHERE state = 'open' AND kind = 'question' AND expires_at IS NULL AND process_id IS NOT NULL;
UPDATE ask SET expires_at = strftime('%Y-%m-%dT%H:%M:%fZ', created_at, '+7 days')
WHERE state = 'open' AND kind = 'login' AND expires_at IS NULL;

-- +goose Down
SELECT 1;
