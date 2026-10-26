-- +goose Up
-- The backdrop of a theme is a picture of its own now: themes lose backdrop and the tokens of the icon pattern.
UPDATE document_revision SET text = replace(replace(replace(replace(replace(replace(text,
        '  "backdrop": "plain",' || char(10), ''), '  "backdrop": "sky",' || char(10), ''), '  "backdrop": "pattern",' || char(10), ''),
        '"backdrop": "plain", ', ''), '"backdrop": "sky", ', ''), '"backdrop": "pattern", ', '')
WHERE document_id IN (SELECT id FROM document WHERE kind = 'theme');

UPDATE document_revision SET text = json_pretty(json_remove(text, '$.backdrop', '$.tokens.pattern', '$.tokens."pattern-alt"',
        '$.light.pattern', '$.light."pattern-alt"', '$.dark.pattern', '$.dark."pattern-alt"'), '  ')
WHERE document_id IN (SELECT id FROM document WHERE kind = 'theme') AND json_valid(text)
    AND (json_type(text, '$.backdrop') IS NOT NULL
        OR json_type(text, '$.tokens.pattern') IS NOT NULL OR json_type(text, '$.tokens."pattern-alt"') IS NOT NULL
        OR json_type(text, '$.light.pattern') IS NOT NULL OR json_type(text, '$.light."pattern-alt"') IS NOT NULL
        OR json_type(text, '$.dark.pattern') IS NOT NULL OR json_type(text, '$.dark."pattern-alt"') IS NOT NULL);

UPDATE document_revision SET text = replace(replace(text, 'var(--pattern-alt)', 'var(--accent-light)'), 'var(--pattern)', 'var(--accent-light)')
WHERE document_id IN (SELECT id FROM document WHERE kind = 'theme') AND text LIKE '%var(--pattern%';

-- +goose Down
SELECT 1;
