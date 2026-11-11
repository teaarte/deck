-- +goose Up
-- The revision of a document a process read or saved last, so a whole text it saves goes only over that, across restarts.
CREATE TABLE IF NOT EXISTS process_read (
    process_id INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE,
    document_id INTEGER NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    revision INTEGER NOT NULL,
    PRIMARY KEY (process_id, document_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS process_read_document ON process_read(document_id);

-- +goose Down
DROP TABLE process_read;
