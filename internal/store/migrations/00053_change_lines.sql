-- +goose Up
-- line is the first change of the same run and module, the one line of the feed they all show in; what is the change
-- without who made it and why, as the line counts it.
ALTER TABLE feed ADD COLUMN line_id INTEGER REFERENCES feed(id) ON DELETE CASCADE;
ALTER TABLE feed ADD COLUMN what TEXT NOT NULL DEFAULT '';

UPDATE feed SET what = CASE
    WHEN command = 'deck.records_upsert' AND text LIKE 'deleted %' THEN 'deleted records of ' || json_extract(params, '$.collection')
    WHEN instr(text, ' by your press: ') > 0 THEN substr(text, 1, instr(text, ' by your press: ') - 1) || ' by your press'
    WHEN instr(text, ' changed ') > 0 AND instr(substr(text, instr(text, ' changed ') + 1), ': ') > 0
        THEN substr(substr(text, instr(text, ' changed ') + 1), 1, instr(substr(text, instr(text, ' changed ') + 1), ': ') - 1)
    WHEN instr(text, ' changed ') > 0 THEN substr(text, instr(text, ' changed ') + 1)
    ELSE text END
WHERE kind = 'change';

UPDATE feed SET line_id = (SELECT min(f.id) FROM feed f WHERE f.kind = 'change' AND f.process_id = feed.process_id
    AND f.project_id = feed.project_id AND f.undone_at IS NULL)
WHERE kind = 'change' AND process_id IS NOT NULL AND undone_at IS NULL;
UPDATE feed SET line_id = NULL WHERE line_id = id;
UPDATE feed SET repeats = 1 + (SELECT count(*) FROM feed f WHERE f.line_id = feed.id) WHERE kind = 'change' AND line_id IS NULL;

CREATE INDEX feed_line ON feed (line_id) WHERE line_id IS NOT NULL;
CREATE INDEX feed_run ON feed (process_id, project_id) WHERE kind = 'change' AND line_id IS NULL AND undone_at IS NULL;

-- +goose Down
SELECT 1;
