-- +goose Up
-- A failure of a process has a type of a closed set and its details apart from the text for the owner; a process
-- that repeats, continues or fixes a failed one follows it.
ALTER TABLE process ADD COLUMN error_type TEXT NOT NULL DEFAULT '';
ALTER TABLE process ADD COLUMN error_details TEXT NOT NULL DEFAULT '';
ALTER TABLE process ADD COLUMN follows_id INTEGER;
CREATE INDEX process_follows ON process (follows_id) WHERE follows_id IS NOT NULL;

UPDATE process SET error_type = CASE
    WHEN state = 'stopped' THEN 'stopped'
    WHEN state = 'interrupted' THEN 'deck.restart'
    WHEN state = 'done' AND not_by_schema = 1 THEN 'result.invalid'
    WHEN error LIKE '%mcp__deck__approve%' OR error LIKE 'the agent started without deck''s tools%' THEN 'deck.tools'
    WHEN error LIKE '%time ceiling%' THEN 'timeout'
    WHEN error LIKE 'program "%' THEN 'program.missing'
    WHEN error LIKE 'script % exited with code%' OR error LIKE 'script % printed%' THEN 'script.failed'
    WHEN error LIKE 'needs login%' THEN 'model.login'
    WHEN error LIKE '%Can''t reach the API%' OR error LIKE '%ENOTFOUND%' THEN 'model.unavailable'
    WHEN error LIKE 'provider reported an error%' THEN 'task.failed'
    ELSE 'deck.internal' END
WHERE state IN ('error', 'stopped', 'interrupted') OR (state = 'done' AND not_by_schema = 1);

-- The same unread note of deck in a project is told once, with how many times it came.
ALTER TABLE feed ADD COLUMN repeats INTEGER NOT NULL DEFAULT 1;

-- +goose StatementBegin
CREATE TRIGGER feed_repeat BEFORE INSERT ON feed
WHEN NEW.author = 'deck' AND NEW.kind = 'note' AND NEW.for_master = 0 AND EXISTS (SELECT 1 FROM feed f
    WHERE f.project_id = NEW.project_id AND f.author = 'deck' AND f.kind = 'note' AND f.for_master = 0
      AND f.read_at IS NULL AND f.text = NEW.text)
BEGIN
    UPDATE feed SET repeats = repeats + 1, created_at = NEW.created_at
    WHERE project_id = NEW.project_id AND author = 'deck' AND kind = 'note' AND for_master = 0 AND read_at IS NULL
      AND text = NEW.text;
    SELECT RAISE(IGNORE);
END;
-- +goose StatementEnd
