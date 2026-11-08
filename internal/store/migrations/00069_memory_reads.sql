-- +goose Up
-- How many times a memory document reached a turn or a search, a signal of what matters to the sleep of the master.
CREATE TABLE IF NOT EXISTS memory_read (
    document_id INTEGER PRIMARY KEY REFERENCES document(id) ON DELETE CASCADE,
    reads INTEGER NOT NULL DEFAULT 0,
    last_read_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS memory_read;
