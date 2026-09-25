-- +goose Up
CREATE TABLE health (
    subject     TEXT PRIMARY KEY,
    status      TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- +goose Down
DROP TABLE health;
