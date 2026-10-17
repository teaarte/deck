-- +goose Up
-- A call of a script, tool or model command for a record while one runs for it waits as the input of one run after.
ALTER TABLE process ADD COLUMN again TEXT NOT NULL DEFAULT '';

-- A stop deck made on its own is deck's.
UPDATE process SET error_type = 'deck.stopped'
WHERE state = 'stopped' AND (error = 'stopped: its record was deleted' OR error = 'deck: the owner deleted the chat topic'
    OR error = 'deck: the process it would continue was closed otherwise');


-- A message deck itself gives a running process, such as another call for its record, is deck's, not the owner's.
ALTER TABLE process_remark ADD COLUMN author TEXT NOT NULL DEFAULT 'owner';
