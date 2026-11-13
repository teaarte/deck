-- +goose Up
-- An automation token reaches modules by rights of deck's catalog, data or modules on a module or one of its commands;
-- its commands are deck's alone. A token that called deck's commands gets the modules it named whole, or every one.
ALTER TABLE automation_token ADD COLUMN rights TEXT NOT NULL DEFAULT '[]';

UPDATE automation_token SET rights = COALESCE((
    SELECT CASE WHEN count(*) > 0 THEN json_array(json_object('right', 'modules', 'what', json_group_array(subject))) END
    FROM (SELECT DISTINCT CASE
            WHEN EXISTS (SELECT 1 FROM json_each(automation_token.commands) d WHERE d.value = '*' OR d.value GLOB 'deck.*')
                THEN substr(j.value, 1, instr(j.value, '.') - 1)
            WHEN j.value GLOB '*.[*]' THEN substr(j.value, 1, length(j.value) - 2)
            ELSE j.value END AS subject
        FROM json_each(automation_token.commands) j WHERE j.value != '*' AND j.value NOT GLOB 'deck.*')), '[]');

UPDATE automation_token SET rights = '[{"right":"modules","what":["*"]}]'
WHERE rights = '[]' AND EXISTS (SELECT 1 FROM json_each(automation_token.commands) d WHERE d.value = '*' OR d.value GLOB 'deck.*');

UPDATE automation_token SET commands = (SELECT json_group_array(CASE WHEN j.value = '*' THEN 'deck.*' ELSE j.value END)
    FROM json_each(automation_token.commands) j WHERE j.value = '*' OR j.value GLOB 'deck.*');

INSERT INTO mark (workspace_id, name, value)
SELECT workspace_id, 'rights:tokens', 'Automation tokens reach modules only by the rights they hold now, as Settings shows: '
    || group_concat(name || ' — ' || (SELECT group_concat(w.value, ', ') FROM json_each(rights) r, json_each(r.value, '$.what') w), '; ')
    || '. A token that called commands of deck reaches the modules it named whole, or every module when it named none: narrow it with a new token.'
FROM automation_token WHERE revoked_at IS NULL AND rights != '[]' GROUP BY workspace_id;

-- +goose Down
UPDATE automation_token SET commands = (SELECT json_group_array(v) FROM (
    SELECT CASE WHEN w.value = '*' OR instr(w.value, '.') > 0 THEN w.value ELSE w.value || '.*' END AS v
    FROM json_each(automation_token.rights) r, json_each(r.value, '$.what') w WHERE json_extract(r.value, '$.right') = 'modules'
    UNION ALL SELECT value FROM json_each(automation_token.commands)));
DELETE FROM mark WHERE name = 'rights:tokens';
ALTER TABLE automation_token DROP COLUMN rights;
