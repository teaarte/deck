-- +goose Up
-- allows are the calls the owner's press named, made without asking in the run it started; wrote is a run that
-- called deck for more than a read or asked, so a run of the schedule that changed nothing is not counted as done.
ALTER TABLE process ADD COLUMN allows TEXT NOT NULL DEFAULT '[]';
ALTER TABLE process ADD COLUMN wrote INTEGER NOT NULL DEFAULT 0;

UPDATE process SET wrote = 1
WHERE EXISTS (SELECT 1 FROM record r WHERE r.process_id = process.id)
   OR EXISTS (SELECT 1 FROM ask a WHERE a.process_id = process.id)
   OR EXISTS (SELECT 1 FROM tool_call t WHERE t.process_id = process.id AND t.outcome != 'rejected'
        AND t.tool NOT LIKE '%\_list' ESCAPE '\' AND t.tool NOT LIKE '%\_get' ESCAPE '\');

CREATE INDEX process_finished ON process (finished_at) WHERE state = 'done';

-- +goose Down
SELECT 1;
