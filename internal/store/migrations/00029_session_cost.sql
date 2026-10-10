-- +goose Up
-- What a session of an agent cost so far, as its CLI counts it, so a resumed turn costs the difference.
ALTER TABLE llm_call ADD COLUMN session_cost_usd REAL;
