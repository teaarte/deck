-- +goose Up
-- Where the cards of the tiles stand on a device for a number of columns: cards is a JSON list of {id, x, y, w, h}
-- in cells, id a project id or 0 for Today. Columns 0 is the order alone, which the order of the menu was.
CREATE TABLE layout (
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    device TEXT NOT NULL,
    columns INTEGER NOT NULL,
    cards TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (workspace_id, device, columns)
);

INSERT INTO layout (workspace_id, device, columns, cards, updated_at)
SELECT workspace_id, '', 0, (SELECT json_group_array(json_object('id', value)) FROM json_each(setting.value)),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
FROM setting WHERE key = 'menu_order' AND json_valid(value);
DELETE FROM setting WHERE key = 'menu_order';

-- +goose Down
SELECT 1;
