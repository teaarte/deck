-- +goose Up
-- The confirmations of processes whose module has not heard yet how they closed (a trigger on ask).
CREATE TABLE IF NOT EXISTS ask_unheard (
    ask_id INTEGER PRIMARY KEY REFERENCES ask(id) ON DELETE CASCADE
);
INSERT OR IGNORE INTO ask_unheard (ask_id)
SELECT id FROM ask WHERE state = 'open' AND process_id IS NOT NULL AND kind IN ('call', 'confirm', 'grant');

-- +goose Down
DROP TABLE IF EXISTS ask_unheard;
