-- +goose Up
-- A pause on a usage limit probes until the limit resets or the owner stops it: it has no end of its own and asks
-- nothing.
ALTER TABLE process DROP COLUMN pause_limit_ms;
UPDATE ask SET state = CASE WHEN state = 'open' THEN 'expired' ELSE state END,
    delivered_at = COALESCE(delivered_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE kind = 'limit';
-- A process paused before goes on through Wake, as a held turn of the same session.
UPDATE process SET wait_fresh = 0, wait_message = '{"message":"deck: the provider''s usage limit should have reset. Continue where you stopped."}'
WHERE state = 'rate_limited';
