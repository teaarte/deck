-- +goose Up
-- deck makes suggestions no more: one still open expires with its call in the journal, and their setting goes.
UPDATE tool_call SET outcome = 'expired', error = 'deck makes suggestions no more'
WHERE outcome = 'waiting' AND ask_id IN (SELECT id FROM ask WHERE kind = 'suggest' AND state = 'open');
UPDATE ask SET state = 'expired', delivered_at = coalesce(delivered_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE kind = 'suggest' AND state = 'open';
DELETE FROM setting WHERE key = 'setting:suggest_days';
