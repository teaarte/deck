-- +goose Up
-- A note may name its cause as the subject of a row of health; unread notes of one cause of a day are one note,
-- the later ones its parts; a note with a screen or buttons stays its own, so its buttons stay.
ALTER TABLE feed ADD COLUMN cause TEXT NOT NULL DEFAULT '';
ALTER TABLE feed ADD COLUMN parts TEXT NOT NULL DEFAULT '[]';

-- +goose StatementBegin
CREATE TRIGGER feed_cause BEFORE INSERT ON feed
WHEN NEW.cause != '' AND NEW.kind = 'note' AND NEW.for_master = 0 AND NEW.screen = '' AND NEW.actions IN ('[]', 'null') AND EXISTS (SELECT 1 FROM feed f
    WHERE f.workspace_id = NEW.workspace_id AND f.cause = NEW.cause AND f.kind = 'note' AND f.for_master = 0
      AND f.read_at IS NULL AND substr(f.created_at, 1, 10) = substr(NEW.created_at, 1, 10))
BEGIN
    UPDATE feed SET repeats = repeats + 1, created_at = NEW.created_at,
        parts = CASE WHEN text = NEW.text AND project_id = NEW.project_id OR EXISTS (SELECT 1 FROM json_each(parts) j
            WHERE json_extract(j.value, '$.text') = NEW.text AND json_extract(j.value, '$.project_id') = NEW.project_id)
            THEN parts ELSE json_insert(parts, '$[#]', json_object('project_id', NEW.project_id, 'author', NEW.author, 'text', NEW.text)) END
    WHERE id = (SELECT f.id FROM feed f WHERE f.workspace_id = NEW.workspace_id AND f.cause = NEW.cause AND f.kind = 'note'
        AND f.for_master = 0 AND f.read_at IS NULL AND substr(f.created_at, 1, 10) = substr(NEW.created_at, 1, 10)
        ORDER BY f.id LIMIT 1);
    SELECT RAISE(IGNORE);
END;
-- +goose StatementEnd
