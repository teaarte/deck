-- +goose Up
-- lane is the stable lane name from the manifest; rank orders cards inside a lane.
CREATE TABLE card (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    text        TEXT NOT NULL DEFAULT '',
    lane        TEXT NOT NULL,
    rank        INTEGER NOT NULL,
    record_id   INTEGER REFERENCES record(id) ON DELETE SET NULL,
    report      TEXT NOT NULL DEFAULT '',
    process_id  INTEGER REFERENCES process(id) ON DELETE SET NULL,
    deleted_at  TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX card_lane ON card (project_id, lane, rank);

CREATE TABLE card_edge (
    card_id    INTEGER NOT NULL REFERENCES card(id) ON DELETE CASCADE,
    waits_for  INTEGER NOT NULL REFERENCES card(id) ON DELETE CASCADE,
    PRIMARY KEY (card_id, waits_for)
);

-- +goose Down
DROP TABLE card_edge;
DROP TABLE card;
