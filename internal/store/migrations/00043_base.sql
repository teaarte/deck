-- +goose Up
-- The schema of migrations 1 to 43; a base of before 43 does not take it.

CREATE TABLE workspace (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL
);
INSERT INTO workspace (id, name) VALUES (1, 'default');

CREATE TABLE project (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'enabled',
    paused INTEGER NOT NULL DEFAULT 0,
    builtin INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    granted_revision INTEGER,
    imported_from TEXT NOT NULL DEFAULT '',
    UNIQUE (workspace_id, name)
);

CREATE TABLE project_folder (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, name),
    UNIQUE (project_id, path)
);

-- A trigger is a schedule or a command another follows; params are the command input.
CREATE TABLE "trigger" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command TEXT NOT NULL,
    kind TEXT NOT NULL,
    spec TEXT NOT NULL DEFAULT '',
    params TEXT NOT NULL DEFAULT '{}',
    next_run_at TEXT,
    paused INTEGER NOT NULL DEFAULT 0,
    skip_missed INTEGER NOT NULL DEFAULT 0,
    last_process_id INTEGER REFERENCES process(id) ON DELETE SET NULL
);

-- The deadline of a process is in ms of the deck clock: ceiling_ms is what is left of its ceiling while it is
-- not counting; pause_until_ms is the next try on a usage limit.
CREATE TABLE process (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'agent',
    state TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    input TEXT NOT NULL DEFAULT '{}',
    result TEXT NOT NULL DEFAULT '',
    not_by_schema INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    start_note TEXT NOT NULL DEFAULT '',
    parent_id INTEGER REFERENCES process(id) ON DELETE SET NULL,
    depth INTEGER NOT NULL DEFAULT 1,
    trigger_id INTEGER REFERENCES "trigger"(id) ON DELETE SET NULL,
    retry_key TEXT NOT NULL DEFAULT '',
    manifest_revision INTEGER NOT NULL DEFAULT 0,
    trial INTEGER NOT NULL DEFAULT 0,
    folder TEXT NOT NULL DEFAULT '',
    pgid INTEGER NOT NULL DEFAULT 0,
    ceiling_ms INTEGER NOT NULL DEFAULT 0,
    deadline_ms INTEGER,
    output_ms INTEGER NOT NULL DEFAULT 0,
    silent_since_ms INTEGER,
    pause_until_ms INTEGER,
    wait_message TEXT NOT NULL DEFAULT '',
    wait_fresh INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT,
    alone INTEGER NOT NULL DEFAULT 0,
    dismissed INTEGER NOT NULL DEFAULT 0,
    reopened_at TEXT,
    error_type TEXT NOT NULL DEFAULT '',
    error_details TEXT NOT NULL DEFAULT '',
    follows_id INTEGER,
    again TEXT NOT NULL DEFAULT ''
);

CREATE INDEX process_project ON process (project_id);

CREATE INDEX process_open ON process (state) WHERE finished_at IS NULL;

CREATE INDEX process_retry ON process (project_id, command, retry_key) WHERE retry_key != '';

-- A message of the owner a running turn could not take; the resume after the turn carries it.
CREATE TABLE process_remark (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    process_id INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE,
    text TEXT NOT NULL,
    created_at TEXT NOT NULL,
    delivered_at TEXT,
    author TEXT NOT NULL DEFAULT 'owner'
);

CREATE INDEX process_remark_process ON process_remark (process_id) WHERE delivered_at IS NULL;

CREATE TABLE folder_lock (
    path TEXT PRIMARY KEY,
    process_id INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE
);

CREATE TABLE record (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    collection TEXT NOT NULL,
    key TEXT NOT NULL,
    data TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    process_id INTEGER,
    UNIQUE (project_id, collection, key)
);

-- The records of a collection as they were before a migration of its schema, so a revert brings them back.
CREATE TABLE record_snapshot (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    collection TEXT NOT NULL,
    from_revision INTEGER NOT NULL,
    revision INTEGER NOT NULL,
    records TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX record_snapshot_from ON record_snapshot (project_id, collection, from_revision);

CREATE TABLE document_revision (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id INTEGER NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    n INTEGER NOT NULL,
    text TEXT NOT NULL,
    author TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (document_id, n)
);

CREATE TABLE file (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    owner_kind TEXT NOT NULL,
    owner_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    process_id INTEGER
);

CREATE INDEX file_owner ON file (owner_kind, owner_id);

-- The journal of gateway calls; caller is the frozen actor, so a call of unknown outcome is reopened for it. The
-- row of a decision deck makes itself (ask_id) keeps its run: running, then settled with the result.
CREATE TABLE tool_call (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES project(id) ON DELETE CASCADE,
    process_id INTEGER REFERENCES process(id) ON DELETE SET NULL,
    actor TEXT NOT NULL DEFAULT '',
    caller TEXT NOT NULL DEFAULT '',
    tool TEXT NOT NULL,
    class TEXT NOT NULL,
    outcome TEXT NOT NULL,
    params TEXT NOT NULL DEFAULT '',
    external_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    ask_id INTEGER REFERENCES ask(id) ON DELETE SET NULL,
    run TEXT NOT NULL DEFAULT '',
    result TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX tool_call_ask ON tool_call (ask_id) WHERE ask_id IS NOT NULL;

CREATE INDEX tool_call_outcome ON tool_call (outcome);

CREATE INDEX tool_call_workspace ON tool_call (workspace_id, id);

CREATE INDEX tool_call_process ON tool_call (process_id);

-- The health of a subject: of the machine without a workspace, of a workspace, or of a project of it.
CREATE TABLE health (
    subject TEXT NOT NULL,
    workspace_id INTEGER REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES project(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    version TEXT NOT NULL DEFAULT '',
    verdict TEXT NOT NULL DEFAULT '',
    speed REAL NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    seen_at TEXT
);

CREATE UNIQUE INDEX health_subject ON health (coalesce(workspace_id, 0), subject);

CREATE TABLE setting (
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (workspace_id, key)
);

-- Where deck has got to: cursors and throttles; a mark of a project goes with it.
CREATE TABLE mark (
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES project(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY (workspace_id, name)
);

CREATE TABLE automation_token (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    hash TEXT NOT NULL UNIQUE,
    commands TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at TEXT
);

-- The tools an MCP server listed; approved is the fingerprint the owner's rights were given for.
CREATE TABLE mcp_tool (
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    server TEXT NOT NULL,
    spec TEXT NOT NULL,
    name TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    input TEXT NOT NULL DEFAULT '{}',
    fingerprint TEXT NOT NULL,
    approved TEXT NOT NULL,
    listed INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (project_id, server, name)
);

-- The owner's feed: a note a script or agent wrote with notify, or a change deck made without the owner, whose
-- command and params undo it while document and revision guard the undo.
CREATE TABLE feed (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('note', 'change')),
    author TEXT NOT NULL,
    text TEXT NOT NULL,
    screen TEXT NOT NULL DEFAULT '',
    record_id INTEGER,
    actions TEXT NOT NULL DEFAULT '[]',
    command TEXT NOT NULL DEFAULT '',
    params TEXT NOT NULL DEFAULT '{}',
    document_id INTEGER REFERENCES document(id) ON DELETE SET NULL,
    revision INTEGER NOT NULL DEFAULT 0,
    undone_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    for_master INTEGER NOT NULL DEFAULT 0,
    read_at TEXT,
    process_id INTEGER,
    repeats INTEGER NOT NULL DEFAULT 1
);

CREATE INDEX feed_workspace ON feed (workspace_id, kind, id);

-- The session a provider keeps of a topic: the last message it saw and the memory it was given.
CREATE TABLE chat_session (
    topic_id INTEGER NOT NULL REFERENCES chat_topic(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    session_id TEXT NOT NULL,
    seen INTEGER NOT NULL DEFAULT 0,
    memory TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (topic_id, provider)
);

CREATE TABLE chat_message (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic_id INTEGER NOT NULL REFERENCES chat_topic(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    text TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    process_id INTEGER NOT NULL DEFAULT 0,
    retry_key TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX chat_message_topic ON chat_message (topic_id, id);

CREATE INDEX chat_message_process ON chat_message (process_id);

CREATE TABLE push_subscription (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    device TEXT NOT NULL,
    endpoint TEXT NOT NULL UNIQUE,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- One model call, named by OpenTelemetry's gen_ai attributes; it outlives its process and project.
CREATE TABLE "llm_call" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES project(id) ON DELETE SET NULL,
    process_id INTEGER REFERENCES process(id) ON DELETE SET NULL,
    command TEXT NOT NULL DEFAULT '',
    gen_ai_provider_name TEXT NOT NULL,
    gen_ai_request_model TEXT NOT NULL DEFAULT '',
    gen_ai_response_model TEXT NOT NULL DEFAULT '',
    gen_ai_usage_input_tokens INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_output_tokens INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_read_input_tokens INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL,
    created_at TEXT NOT NULL,
    connection_id INTEGER REFERENCES connection(id) ON DELETE SET NULL,
    session_cost_usd REAL,
    subscription INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX llm_call_process ON llm_call (process_id);

CREATE INDEX llm_call_workspace ON llm_call (workspace_id, created_at);

-- A decision of the owner: open, then answered (a confirmation then used) or expired (then dismissed); delivered_at
-- is when the waiting process was told; expires_at is a hard deadline only.
CREATE TABLE "ask" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES project(id) ON DELETE CASCADE,
    process_id INTEGER REFERENCES process(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    text TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    answer TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'open',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    answered_at TEXT,
    expires_at TEXT,
    delivered_at TEXT,
    unheard_at TEXT
);

CREATE INDEX ask_process ON ask (process_id, state);

CREATE INDEX ask_workspace ON ask (workspace_id, state);

CREATE TABLE connection (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    api TEXT NOT NULL DEFAULT 'openai',
    url TEXT NOT NULL,
    key_secret TEXT NOT NULL DEFAULT '',
    local INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (workspace_id, name)
);

-- agent is a provider of agent turns by its name. An assignment names the models of a kind of work, the first one
-- first, in the workspace, a project or a command.
CREATE TABLE model (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    connection_id INTEGER NOT NULL REFERENCES connection(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    model TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT 'text',
    agent TEXT NOT NULL DEFAULT '',
    input_usd REAL,
    output_usd REAL,
    call_usd REAL,
    free INTEGER NOT NULL DEFAULT 0,
    options TEXT NOT NULL DEFAULT '{}',
    check_minutes INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    unit TEXT NOT NULL DEFAULT 'token',
    subscription INTEGER NOT NULL DEFAULT 0,
    UNIQUE (workspace_id, name)
);

CREATE INDEX model_connection ON model (connection_id);

CREATE TABLE assignment (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES project(id) ON DELETE CASCADE,
    command TEXT NOT NULL DEFAULT '',
    role TEXT NOT NULL,
    models TEXT NOT NULL DEFAULT '[]'
);

CREATE UNIQUE INDEX assignment_place ON assignment (workspace_id, coalesce(project_id, 0), command, role);

CREATE INDEX llm_call_connection ON llm_call (connection_id, created_at);

CREATE UNIQUE INDEX trigger_identity ON "trigger" (project_id, command, kind, spec);

-- A document belongs to a project, or to the workspace without one; a record of the project may own it.
-- record_id has no foreign key: a record a collection migration drops comes back with its id on a revert.
CREATE TABLE "document" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id INTEGER REFERENCES project(id) ON DELETE CASCADE,
    record_id INTEGER,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    current INTEGER NOT NULL DEFAULT 0,
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE UNIQUE INDEX document_name ON document (workspace_id, coalesce(project_id, 0), coalesce(record_id, 0), name);

CREATE TABLE "chat_topic" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    archived INTEGER NOT NULL DEFAULT 0,
    deleted_at TEXT,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    model TEXT NOT NULL DEFAULT ''
);

CREATE INDEX chat_topic_workspace ON chat_topic (workspace_id, updated_at);

-- What deck does after a commit: the files service and the runner drain it after the commit, on every tick
-- and at start.
CREATE TABLE outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    ref INTEGER NOT NULL,
    tries INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    path TEXT NOT NULL DEFAULT ''
);

-- +goose StatementBegin
CREATE TRIGGER file_gone AFTER DELETE ON file BEGIN
    INSERT INTO outbox (kind, ref) VALUES ('file', OLD.id);
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER process_closed AFTER UPDATE OF finished_at ON process
WHEN OLD.finished_at IS NULL AND NEW.finished_at IS NOT NULL BEGIN
    INSERT INTO outbox (kind, ref) VALUES ('closed', NEW.id);
END;
-- +goose StatementEnd

-- A lookup of a document by its project and name, as the queries ask it; the unique index of names is by coalesce.
CREATE INDEX document_lookup ON document (workspace_id, project_id, name);

-- How many writes the records of a project have seen, counted by the triggers of record.
CREATE TABLE record_mark (
    project_id INTEGER PRIMARY KEY,
    n INTEGER NOT NULL DEFAULT 0
);

-- +goose StatementBegin
CREATE TRIGGER record_added AFTER INSERT ON record BEGIN
    INSERT INTO record_mark (project_id, n) VALUES (NEW.project_id, 1) ON CONFLICT (project_id) DO UPDATE SET n = n + 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER record_changed AFTER UPDATE ON record BEGIN
    INSERT INTO record_mark (project_id, n) VALUES (NEW.project_id, 1) ON CONFLICT (project_id) DO UPDATE SET n = n + 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER record_gone AFTER DELETE ON record BEGIN
    INSERT INTO record_mark (project_id, n) VALUES (OLD.project_id, 1) ON CONFLICT (project_id) DO UPDATE SET n = n + 1;
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER project_gone AFTER DELETE ON project BEGIN
    INSERT INTO outbox (kind, ref) VALUES ('project', OLD.id);
END;
-- +goose StatementEnd

CREATE INDEX process_follows ON process (follows_id) WHERE follows_id IS NOT NULL;

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

-- The whisper.cpp of the machine is the model of speech to text; the CLIs on the subscription are connections.
INSERT INTO connection (workspace_id, name, api, url, local)
VALUES (1, 'whisper', 'raw', 'http://127.0.0.1:8178/inference', 1);
INSERT INTO model (workspace_id, connection_id, name, model, kind, free, options)
VALUES (1, 1, 'whisper', 'whisper', 'stt', 1, '{"language": "auto"}');
INSERT INTO assignment (workspace_id, command, role, models) VALUES (1, '', 'stt', '["whisper"]');
INSERT INTO connection (workspace_id, name, api, url) VALUES (1, 'claude-code', 'cli', ''), (1, 'codex', 'cli', '');
