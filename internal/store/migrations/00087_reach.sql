-- +goose Up
-- A line of the journal says the subject of the right it used, and how many times: the door writes a host its programs
-- reached once per run with the number of requests.
ALTER TABLE tool_call ADD COLUMN subject TEXT NOT NULL DEFAULT '';
ALTER TABLE tool_call ADD COLUMN count INTEGER NOT NULL DEFAULT 1;
CREATE INDEX tool_call_project ON tool_call (project_id, created_at);
-- What a running process holds open: the hosts of its box and the login of its CLI, as JSON.
ALTER TABLE process ADD COLUMN opens TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE process DROP COLUMN opens;
DROP INDEX tool_call_project;
ALTER TABLE tool_call DROP COLUMN count;
ALTER TABLE tool_call DROP COLUMN subject;
