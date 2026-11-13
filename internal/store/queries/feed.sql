-- name: AddFeed :one
INSERT INTO feed (workspace_id, project_id, kind, author, text, screen, record_id, actions, command, params, document_id, revision, created_at, process_id,
    cause, what, line_id)
VALUES (sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(author), sqlc.arg(text), sqlc.arg(screen),
    sqlc.narg(record_id), sqlc.arg(actions), sqlc.arg(command), sqlc.arg(params), sqlc.narg(document_id), sqlc.arg(revision), sqlc.arg(created_at),
    sqlc.narg(process_id), sqlc.arg(cause), sqlc.arg(what), sqlc.narg(line_id))
RETURNING *;

-- name: ChangeLine :one
SELECT id FROM feed WHERE kind = 'change' AND process_id = sqlc.arg(process_id) AND project_id = sqlc.arg(project_id) AND line_id IS NULL
  AND undone_at IS NULL ORDER BY id LIMIT 1;

-- name: JoinChangeLine :exec
UPDATE feed SET repeats = repeats + 1, created_at = sqlc.arg(created_at) WHERE id = sqlc.arg(id);

-- name: ListChangeLine :many
SELECT * FROM feed WHERE id = sqlc.arg(id) OR line_id = sqlc.arg(id) ORDER BY id;

-- name: LeaveChangeLine :exec
UPDATE feed SET repeats = repeats - 1 WHERE id = sqlc.arg(id);

-- name: HandChangeLine :exec
-- The first change after the head leads the line of the others.
UPDATE feed SET line_id = CASE WHEN id = sqlc.arg(heir) THEN NULL ELSE sqlc.arg(heir) END,
    repeats = CASE WHEN id = sqlc.arg(heir) THEN sqlc.arg(repeats) ELSE repeats END
WHERE line_id = sqlc.arg(id);

-- name: DeleteFeed :exec
DELETE FROM feed WHERE id = ?;

-- name: CauseNote :one
SELECT id FROM feed WHERE workspace_id = sqlc.arg(workspace_id) AND cause = sqlc.arg(cause) AND kind = 'note' AND for_master = 0
    AND read_at IS NULL AND substr(created_at, 1, 10) = substr(CAST(sqlc.arg(created_at) AS TEXT), 1, 10)
ORDER BY id LIMIT 1;

-- name: JoinCauseNote :exec
-- A part of the same text and project as the note or one of its parts is not listed twice.
UPDATE feed SET repeats = repeats + 1, created_at = sqlc.arg(created_at),
    parts = CASE WHEN text = sqlc.arg(text) AND project_id = sqlc.arg(project_id) OR EXISTS (SELECT 1 FROM json_each(parts) j
        WHERE json_extract(j.value, '$.text') = sqlc.arg(text) AND json_extract(j.value, '$.project_id') = sqlc.arg(project_id))
        THEN parts ELSE json_insert(parts, '$[#]', json_object('project_id', sqlc.arg(project_id), 'author', CAST(sqlc.arg(author) AS TEXT), 'text', sqlc.arg(text))) END
WHERE id = sqlc.arg(id);

-- name: RepeatDeckNote :many
UPDATE feed SET repeats = repeats + 1, created_at = sqlc.arg(created_at)
WHERE project_id = sqlc.arg(project_id) AND author = 'deck' AND kind = 'note' AND for_master = 0 AND read_at IS NULL
  AND text = sqlc.arg(text)
RETURNING id;

-- name: ListFeed :many
SELECT * FROM feed WHERE workspace_id = sqlc.arg(workspace_id) AND (CAST(sqlc.arg(project_id) AS INTEGER) = 0 OR project_id = CAST(sqlc.arg(project_id) AS INTEGER))
  AND (CAST(sqlc.arg(kind) AS TEXT) = '' OR kind = CAST(sqlc.arg(kind) AS TEXT)) AND for_master = 0 AND (CAST(sqlc.arg(unread) AS INTEGER) = 0 OR kind = 'note' AND read_at IS NULL)
  AND line_id IS NULL
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(lim);

-- name: GetFeed :one
SELECT * FROM feed WHERE id = ?;

-- name: MarkFeedUndone :execrows
UPDATE feed SET undone_at = CAST(sqlc.arg(now) AS TEXT) WHERE id = sqlc.arg(id) AND undone_at IS NULL;

-- name: PurgeOldNotes :exec
DELETE FROM feed WHERE kind = 'note' AND for_master = 0 AND (read_at IS NOT NULL OR author != 'deck')
  AND created_at < CAST(sqlc.arg(before) AS TEXT);

-- name: AddMasterNote :exec
-- A note of deck for the master, with the process whose words it quotes.
INSERT INTO feed (workspace_id, project_id, kind, author, text, for_master, created_at, process_id) VALUES (?, ?, 'note', 'deck', ?, 1, ?, ?);

-- name: ListMasterNotes :many
SELECT * FROM feed WHERE workspace_id = ? AND for_master = 1 ORDER BY id;

-- name: DeleteMasterNotes :exec
DELETE FROM feed WHERE workspace_id = sqlc.arg(workspace_id) AND for_master = 1 AND id <= sqlc.arg(through);

-- name: ReadNotesOfProcess :exec
UPDATE feed SET read_at = CAST(sqlc.arg(now) AS TEXT)
WHERE process_id = sqlc.arg(process_id) AND kind = 'note' AND for_master = 0 AND read_at IS NULL;


-- name: MarkFeedRead :execrows
UPDATE feed SET read_at = CAST(sqlc.arg(now) AS TEXT)
WHERE id = sqlc.arg(id) AND workspace_id = sqlc.arg(workspace_id) AND kind = 'note' AND for_master = 0 AND read_at IS NULL;


-- name: MarkAllFeedRead :execrows
UPDATE feed SET read_at = CAST(sqlc.arg(now) AS TEXT)
WHERE workspace_id = sqlc.arg(workspace_id) AND kind = 'note' AND for_master = 0 AND read_at IS NULL;

-- name: CountUnreadNotes :one
SELECT CAST(count(*) AS INTEGER) FROM feed WHERE workspace_id = sqlc.arg(workspace_id) AND kind = 'note' AND for_master = 0 AND read_at IS NULL;
