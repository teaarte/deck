-- +goose Up
CREATE TABLE setting (
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    value         TEXT NOT NULL,
    PRIMARY KEY (workspace_id, key)
);

-- Field names follow the OpenTelemetry gen_ai attributes; cost_usd is what the provider reports, notional on a subscription.
CREATE TABLE llm_call (
    id                                        INTEGER PRIMARY KEY,
    process_id                                INTEGER NOT NULL,
    gen_ai_provider_name                      TEXT NOT NULL,
    gen_ai_request_model                      TEXT NOT NULL DEFAULT '',
    gen_ai_response_model                     TEXT NOT NULL DEFAULT '',
    gen_ai_usage_input_tokens                 INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_output_tokens                INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_read_input_tokens      INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_creation_input_tokens  INTEGER NOT NULL DEFAULT 0,
    duration_ms                               INTEGER NOT NULL DEFAULT 0,
    cost_usd                                  REAL,
    created_at                                TEXT NOT NULL
);

CREATE INDEX llm_call_process ON llm_call (process_id);

-- pause_until_ms is the next try of a process paused on a provider limit, pause_limit_ms the end of that pause;
-- wait_message is what a process waiting for its provider sends once the provider is back.
ALTER TABLE process ADD COLUMN pause_until_ms INTEGER;
ALTER TABLE process ADD COLUMN pause_limit_ms INTEGER;
ALTER TABLE process ADD COLUMN wait_message TEXT NOT NULL DEFAULT '';
ALTER TABLE process ADD COLUMN wait_fresh INTEGER NOT NULL DEFAULT 0;

-- version is the version of the program behind a subject, such as a provider CLI, as its last check saw it.
ALTER TABLE health ADD COLUMN version TEXT NOT NULL DEFAULT '';

-- +goose Down
DROP TABLE llm_call;
DROP TABLE setting;
