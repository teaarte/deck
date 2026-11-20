-- +goose Up
-- The key a caller gave a call, the slot of a trigger or a press of the owner: a second call with it gives what the
-- first gave. A key is kept a day.
CREATE TABLE call_key (
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    caller TEXT NOT NULL,
    key TEXT NOT NULL,
    command TEXT NOT NULL,
    state TEXT NOT NULL,
    result TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (workspace_id, caller, key)
);

CREATE INDEX call_key_created ON call_key (created_at);

-- +goose Down
DROP TABLE call_key;
