-- name: CreateAsk :one
INSERT INTO ask (process_id, workspace_id, project_id, kind, text, payload, expires_at)
SELECT p.id, j.workspace_id, j.id, CAST(sqlc.arg(kind) AS TEXT), CAST(sqlc.arg(text) AS TEXT), CAST(sqlc.arg(payload) AS TEXT),
    sqlc.narg(expires_at)
FROM process p JOIN project j ON j.id = p.project_id WHERE p.id = sqlc.arg(process_id)
RETURNING *;


-- name: CreateProjectAsk :one
INSERT INTO ask (process_id, workspace_id, project_id, kind, text, payload, expires_at)
VALUES (sqlc.narg(process_id), sqlc.arg(workspace_id), sqlc.arg(project_id), sqlc.arg(kind), sqlc.arg(text), sqlc.arg(payload),
    sqlc.narg(expires_at)) RETURNING *;


-- name: GetAsk :one
SELECT * FROM ask WHERE id = ?;


-- name: ListOpenCallAsks :many
SELECT * FROM ask WHERE state = 'open' AND kind = 'call' ORDER BY id;


-- name: ListWorkspaceOpenAsks :many
SELECT * FROM ask WHERE state = 'open' AND workspace_id = ? ORDER BY id;


-- name: DismissShownQuestion :exec
-- The question of a block of the chat another block took the place of.
UPDATE ask SET state = 'dismissed'
WHERE kind = 'question' AND state = 'open' AND process_id IS NULL
  AND json_extract(payload, '$.message') = CAST(sqlc.arg(message) AS INTEGER);


-- name: DismissTopicQuestions :exec
-- The questions of the blocks of a topic the owner wrote past in words.
UPDATE ask SET state = 'dismissed'
WHERE kind = 'question' AND state = 'open' AND process_id IS NULL AND json_extract(payload, '$.message') IN (
  SELECT m.id FROM chat_message m WHERE m.topic_id = CAST(sqlc.arg(topic_id) AS INTEGER));


-- name: SetQuestionText :exec
-- The words of a question the owner reads, in its text and its first question, while it is open.
UPDATE ask SET text = sqlc.arg(text), payload = json_set(payload, '$.questions[0].question', sqlc.arg(text))
WHERE id = sqlc.arg(id) AND state = 'open';


-- name: AnswerAsk :execrows
UPDATE ask SET answer = ?, state = 'answered'
WHERE id = ? AND state = 'open';


-- name: ListOpenAsksOfProcess :many
SELECT * FROM ask a
WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER)
  AND (a.state = 'open' OR EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running'))
ORDER BY a.id;


-- name: ListUndeliveredAsks :many
SELECT sqlc.embed(a),
    CAST(coalesce((SELECT t.result FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'settled' ORDER BY t.id DESC LIMIT 1), '') AS TEXT) AS result
FROM ask a
WHERE a.process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND a.state IN ('answered', 'expired', 'dismissed') AND a.delivered_at IS NULL
  AND NOT EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running')
ORDER BY a.id;


-- name: CloseOpenAsksOfProcess :many
-- A request of the turn or of its start ends with the process; a frozen call outlives it unless calls_too.
UPDATE ask SET state = 'expired', delivered_at = CAST(sqlc.arg(now) AS TEXT)
WHERE process_id = CAST(sqlc.arg(process_id) AS INTEGER) AND state = 'open' AND kind != 'unknown'
  AND (CAST(sqlc.arg(calls_too) AS INTEGER) = 1 OR kind != 'call' OR json_extract(payload, '$.as') IN ('turn', 'start'))
RETURNING *;


-- name: ExpireStaleAsks :many
-- The asks past their term: requests about calls, and the questions and sign-ins a process waits on.
UPDATE ask SET state = 'expired'
WHERE (kind = 'call' OR kind IN ('question', 'sign_in') AND process_id IS NOT NULL)
  AND state = 'open' AND expires_at <= CAST(sqlc.arg(now) AS TEXT) RETURNING *;


-- name: ListOpenProjectAsks :many
SELECT * FROM ask WHERE project_id IS sqlc.arg(project_id) AND process_id IS NULL AND state = 'open' ORDER BY id;


-- name: ListRecentRequests :many
SELECT project_id, created_at, shown_at, answered_at, answer FROM ask
WHERE workspace_id = sqlc.arg(workspace_id) AND kind = 'call' AND created_at >= CAST(sqlc.arg(since) AS TEXT) ORDER BY id;


-- name: ShowAsks :exec
UPDATE ask SET shown_at = CAST(sqlc.arg(now) AS TEXT) WHERE workspace_id = sqlc.arg(workspace_id) AND state = 'open' AND shown_at IS NULL;


-- name: FindOpenCallAsk :one
SELECT * FROM ask
WHERE kind = 'call' AND state = 'open' AND coalesce(project_id, 0) = CAST(sqlc.arg(project_id) AS INTEGER) AND payload = sqlc.arg(payload)
  AND coalesce(process_id, 0) = CAST(sqlc.arg(process_id) AS INTEGER)
ORDER BY id LIMIT 1;


-- name: ClaimDecision :execrows
UPDATE ask SET state = 'answered', answer = sqlc.arg(answer)
WHERE id = sqlc.arg(id) AND state = 'open' AND (expires_at IS NULL OR expires_at > CAST(sqlc.arg(now) AS TEXT));


-- name: ReopenDecision :exec
UPDATE ask SET state = 'open', answer = '' WHERE id = ? AND state = 'answered';


-- name: ListWorkspaceExpiredConfirms :many
SELECT * FROM ask
WHERE workspace_id = sqlc.arg(workspace_id) AND state = 'expired' AND kind = 'call'
  AND expires_at <= CAST(sqlc.arg(now) AS TEXT)
ORDER BY id;


-- name: DismissExpiredAsk :execrows
UPDATE ask SET state = 'dismissed' WHERE id = ? AND state = 'expired';


-- name: CreateRemark :one
INSERT INTO process_remark (process_id, text, created_at)
SELECT p.id, CAST(sqlc.arg(text) AS TEXT), CAST(sqlc.arg(created_at) AS TEXT) FROM process p
WHERE p.id = sqlc.arg(process_id) AND p.finished_at IS NULL
RETURNING *;


-- name: ListUndeliveredRemarks :many
SELECT * FROM process_remark WHERE process_id = ? AND delivered_at IS NULL ORDER BY id;


-- name: MarkRemarkDelivered :exec
UPDATE process_remark SET delivered_at = CAST(sqlc.arg(now) AS TEXT) WHERE id = sqlc.arg(id) AND delivered_at IS NULL;


-- name: LastAskOfProcess :one
SELECT * FROM ask WHERE process_id = ? ORDER BY id DESC LIMIT 1;


-- name: DeliverAsk :execrows
UPDATE ask SET delivered_at = CAST(sqlc.arg(now) AS TEXT) WHERE id = sqlc.arg(id) AND delivered_at IS NULL;


-- name: ExpireAsk :execrows
UPDATE ask SET state = 'expired' WHERE id = ? AND state = 'open';


-- name: TellUnheard :execrows
UPDATE ask SET unheard_at = CAST(sqlc.arg(now) AS TEXT) WHERE id = sqlc.arg(id) AND unheard_at IS NULL;

-- name: CreateTakenRemark :one
INSERT INTO process_remark (process_id, text, created_at, delivered_at)
SELECT p.id, CAST(sqlc.arg(text) AS TEXT), CAST(sqlc.arg(created_at) AS TEXT), CAST(sqlc.arg(created_at) AS TEXT) FROM process p
WHERE p.id = sqlc.arg(process_id) AND p.finished_at IS NULL
RETURNING id;


-- name: ReturnRemark :exec
UPDATE process_remark SET delivered_at = NULL WHERE id = ?;


-- name: CreateDeckRemark :one
INSERT INTO process_remark (process_id, text, created_at, delivered_at, author) VALUES (?, ?, ?, ?, 'deck') RETURNING id;


-- name: SetRemarkText :exec
UPDATE process_remark SET text = ? WHERE id = ?;


-- name: ListRemarks :many
SELECT * FROM process_remark WHERE process_id = ? ORDER BY id;


-- name: DeleteWaitingRemark :execrows
DELETE FROM process_remark WHERE id = ? AND process_id = ? AND delivered_at IS NULL;


-- name: ListAnsweredAsksOfProcess :many
SELECT kind, text, answer FROM ask WHERE process_id = ? AND state = 'answered' AND answer != '' ORDER BY id;


-- name: AddUnheardAsk :exec
INSERT OR IGNORE INTO ask_unheard (ask_id) VALUES (?);


-- name: ListUnheardAsks :many
-- The closed confirmations whose module has not heard how they closed; a call deck is still making is not closed yet.
SELECT a.* FROM ask_unheard u JOIN ask a ON a.id = u.ask_id
WHERE a.state != 'open' AND NOT EXISTS (SELECT 1 FROM tool_call t WHERE t.ask_id = a.id AND t.run = 'running')
ORDER BY a.id;


-- name: HearAsk :exec
DELETE FROM ask_unheard WHERE ask_id = ?;


-- name: ListAsksOfProcess :many
SELECT * FROM ask WHERE process_id = ? ORDER BY id DESC;
