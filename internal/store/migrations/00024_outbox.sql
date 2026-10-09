-- +goose Up
-- What deck does once a write committed: remove the file of a deleted row, hear a process that closed. The intent
-- commits with the write, a row goes when its work is done, and deck does what is left after a restart.
CREATE TABLE outbox (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT NOT NULL,
    ref         INTEGER NOT NULL,
    tries       INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- +goose StatementBegin
CREATE TRIGGER file_gone AFTER DELETE ON file BEGIN
    INSERT INTO outbox (kind, ref) VALUES ('file', OLD.id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER process_closed AFTER UPDATE OF finished_at ON process
WHEN OLD.finished_at IS NULL AND NEW.finished_at IS NOT NULL BEGIN
    INSERT INTO outbox (kind, ref) VALUES ('closed', NEW.id);
END;
-- +goose StatementEnd
