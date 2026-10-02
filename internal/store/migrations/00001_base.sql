-- +goose Up
CREATE TABLE workspace (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    name  TEXT NOT NULL
);
INSERT INTO workspace (id, name) VALUES (1, 'default');

CREATE TABLE project (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'enabled',
    paused        INTEGER NOT NULL DEFAULT 0,
    builtin       INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (workspace_id, name)
);

CREATE TABLE project_folder (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, name),
    UNIQUE (project_id, path)
);

-- A trigger is a schedule, a lane, a command another follows, or a run later; params are the command input.
CREATE TABLE "trigger" (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id       INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command          TEXT NOT NULL,
    kind             TEXT NOT NULL,
    spec             TEXT NOT NULL DEFAULT '',
    params           TEXT NOT NULL DEFAULT '{}',
    next_run_at      TEXT,
    paused           INTEGER NOT NULL DEFAULT 0,
    skip_missed      INTEGER NOT NULL DEFAULT 0,
    last_process_id  INTEGER REFERENCES process(id) ON DELETE SET NULL
);
CREATE UNIQUE INDEX trigger_identity ON "trigger" (project_id, command, kind, spec) WHERE kind != 'once';

-- The deadline of a process is in ms of the deck clock: ceiling_ms is what is left of its ceiling while it is
-- not counting; pause_until_ms is the next try on a usage limit and pause_limit_ms the end of that pause.
CREATE TABLE process (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id         INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command            TEXT NOT NULL,
    kind               TEXT NOT NULL DEFAULT 'agent',
    state              TEXT NOT NULL,
    provider           TEXT NOT NULL DEFAULT '',
    session_id         TEXT NOT NULL DEFAULT '',
    input              TEXT NOT NULL DEFAULT '{}',
    result             TEXT NOT NULL DEFAULT '',
    not_by_schema      INTEGER NOT NULL DEFAULT 0,
    error              TEXT NOT NULL DEFAULT '',
    start_note         TEXT NOT NULL DEFAULT '',
    parent_id          INTEGER REFERENCES process(id) ON DELETE SET NULL,
    depth              INTEGER NOT NULL DEFAULT 1,
    trigger_id         INTEGER REFERENCES "trigger"(id) ON DELETE SET NULL,
    retry_key          TEXT NOT NULL DEFAULT '',
    manifest_revision  INTEGER NOT NULL DEFAULT 0,
    trial              INTEGER NOT NULL DEFAULT 0,
    folder             TEXT NOT NULL DEFAULT '',
    pgid               INTEGER NOT NULL DEFAULT 0,
    ceiling_ms         INTEGER NOT NULL DEFAULT 0,
    deadline_ms        INTEGER,
    output_ms          INTEGER NOT NULL DEFAULT 0,
    silent_since_ms    INTEGER,
    pause_until_ms     INTEGER,
    pause_limit_ms     INTEGER,
    wait_message       TEXT NOT NULL DEFAULT '',
    wait_fresh         INTEGER NOT NULL DEFAULT 0,
    started_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at        TEXT
);
CREATE INDEX process_project ON process (project_id);
CREATE INDEX process_open ON process (state) WHERE finished_at IS NULL;
CREATE INDEX process_retry ON process (project_id, command, retry_key) WHERE retry_key != '';

-- A message of the owner a running turn could not take; the resume after the turn carries it.
CREATE TABLE process_remark (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    process_id    INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE,
    text          TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    delivered_at  TEXT
);
CREATE INDEX process_remark_process ON process_remark (process_id) WHERE delivered_at IS NULL;

CREATE TABLE folder_lock (
    path        TEXT PRIMARY KEY,
    process_id  INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE
);

CREATE TABLE record (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    collection  TEXT NOT NULL,
    key         TEXT NOT NULL,
    data        TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (project_id, collection, key)
);

-- The records of a collection as they were before a migration of its schema, so a revert brings them back.
CREATE TABLE record_snapshot (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id     INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    collection     TEXT NOT NULL,
    from_revision  INTEGER NOT NULL,
    revision       INTEGER NOT NULL,
    records        TEXT NOT NULL,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX record_snapshot_from ON record_snapshot (project_id, collection, from_revision);

CREATE TABLE card (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    text        TEXT NOT NULL DEFAULT '',
    lane        TEXT NOT NULL,
    rank        INTEGER NOT NULL,
    record_id   INTEGER REFERENCES record(id) ON DELETE SET NULL,
    process_id  INTEGER REFERENCES process(id) ON DELETE SET NULL,
    deleted_at  TEXT,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX card_lane ON card (project_id, lane, rank);

CREATE TABLE card_edge (
    card_id    INTEGER NOT NULL REFERENCES card(id) ON DELETE CASCADE,
    waits_for  INTEGER NOT NULL REFERENCES card(id) ON DELETE CASCADE,
    PRIMARY KEY (card_id, waits_for)
);

-- A document belongs to a project, or to the workspace without one; a record or a card of the project may own it.
-- record_id has no foreign key: a record a collection migration drops comes back with its id on a revert.
CREATE TABLE document (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER REFERENCES project(id) ON DELETE CASCADE,
    record_id     INTEGER,
    card_id       INTEGER REFERENCES card(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    name          TEXT NOT NULL,
    current       INTEGER NOT NULL DEFAULT 0,
    deleted_at    TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX document_name ON document (workspace_id, coalesce(project_id, 0), coalesce(record_id, 0), coalesce(card_id, 0), name);

CREATE TABLE document_revision (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    document_id  INTEGER NOT NULL REFERENCES document(id) ON DELETE CASCADE,
    n            INTEGER NOT NULL,
    text         TEXT NOT NULL,
    author       TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (document_id, n)
);

CREATE TABLE file (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    owner_kind  TEXT NOT NULL,
    owner_id    INTEGER NOT NULL,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL DEFAULT '',
    size        INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX file_owner ON file (owner_kind, owner_id);

-- A decision of the owner: open, then answered (a confirmation then used) or expired (then dismissed); delivered_at
-- is when the waiting process was told; expires_at is a hard deadline only.
CREATE TABLE ask (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    process_id    INTEGER REFERENCES process(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,
    text          TEXT NOT NULL,
    payload       TEXT NOT NULL DEFAULT '{}',
    answer        TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'open',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    answered_at   TEXT,
    expires_at    TEXT,
    delivered_at  TEXT
);
CREATE INDEX ask_process ON ask (process_id, state);
CREATE INDEX ask_workspace ON ask (workspace_id, state);

-- The journal of gateway calls; caller is the frozen actor, so a call of unknown outcome is reopened for it. The
-- row of a decision deck makes itself (ask_id) keeps its run: running, then settled with the result.
CREATE TABLE tool_call (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER REFERENCES project(id) ON DELETE CASCADE,
    process_id    INTEGER REFERENCES process(id) ON DELETE SET NULL,
    actor         TEXT NOT NULL DEFAULT '',
    caller        TEXT NOT NULL DEFAULT '',
    tool          TEXT NOT NULL,
    class         TEXT NOT NULL,
    outcome       TEXT NOT NULL,
    params        TEXT NOT NULL DEFAULT '',
    external_id   TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    ask_id        INTEGER REFERENCES ask(id) ON DELETE SET NULL,
    run           TEXT NOT NULL DEFAULT '',
    result        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX tool_call_ask ON tool_call (ask_id) WHERE ask_id IS NOT NULL;
CREATE INDEX tool_call_outcome ON tool_call (outcome);
CREATE INDEX tool_call_workspace ON tool_call (workspace_id, id);
CREATE INDEX tool_call_process ON tool_call (process_id);

-- One model call of a process, named by OpenTelemetry's gen_ai attributes.
CREATE TABLE llm_call (
    id                                        INTEGER PRIMARY KEY AUTOINCREMENT,
    process_id                                INTEGER NOT NULL REFERENCES process(id) ON DELETE CASCADE,
    gen_ai_provider_name                      TEXT NOT NULL,
    gen_ai_request_model                      TEXT NOT NULL DEFAULT '',
    gen_ai_response_model                     TEXT NOT NULL DEFAULT '',
    gen_ai_usage_input_tokens                 INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_output_tokens                INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_read_input_tokens      INTEGER NOT NULL DEFAULT 0,
    gen_ai_usage_cache_creation_input_tokens  INTEGER NOT NULL DEFAULT 0,
    duration_ms                               INTEGER NOT NULL DEFAULT 0,
    cost_usd                                  REAL,
    created_at                                TEXT NOT NULL
);
CREATE INDEX llm_call_process ON llm_call (process_id);

CREATE TABLE health (
    subject     TEXT PRIMARY KEY,
    status      TEXT NOT NULL,
    error       TEXT NOT NULL DEFAULT '',
    version     TEXT NOT NULL DEFAULT '',
    verdict     TEXT NOT NULL DEFAULT '',
    speed       REAL NOT NULL DEFAULT 0,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE setting (
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    value         TEXT NOT NULL,
    PRIMARY KEY (workspace_id, key)
);

-- The provider the owner set for an agent command; without a row the command takes the default.
CREATE TABLE command_provider (
    project_id  INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    command     TEXT NOT NULL,
    provider    TEXT NOT NULL,
    PRIMARY KEY (project_id, command)
);

-- Where deck has got to: the cursors of push and the last hand-over of a command to the master; a mark of a
-- project goes with it.
CREATE TABLE mark (
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER REFERENCES project(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    value         TEXT NOT NULL,
    PRIMARY KEY (workspace_id, name)
);

CREATE TABLE automation_token (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    hash          TEXT NOT NULL UNIQUE,
    commands      TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at    TEXT
);

-- What deck did without the owner; command and params undo it, document and revision guard the undo.
CREATE TABLE unattended (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    author        TEXT NOT NULL,
    text          TEXT NOT NULL,
    command       TEXT NOT NULL DEFAULT '',
    params        TEXT NOT NULL DEFAULT '{}',
    document_id   INTEGER REFERENCES document(id) ON DELETE SET NULL,
    revision      INTEGER NOT NULL DEFAULT 0,
    undone_at     TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- The tools an MCP server listed; approved is the fingerprint the owner's rights were given for.
CREATE TABLE mcp_tool (
    project_id   INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    server       TEXT NOT NULL,
    spec         TEXT NOT NULL,
    name         TEXT NOT NULL,
    title        TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    input        TEXT NOT NULL DEFAULT '{}',
    fingerprint  TEXT NOT NULL,
    approved     TEXT NOT NULL,
    listed       INTEGER NOT NULL DEFAULT 1,
    PRIMARY KEY (project_id, server, name)
);

CREATE TABLE notification (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    project_id    INTEGER NOT NULL REFERENCES project(id) ON DELETE CASCADE,
    author        TEXT NOT NULL,
    text          TEXT NOT NULL,
    screen        TEXT NOT NULL DEFAULT '',
    record_id     INTEGER,
    actions       TEXT NOT NULL DEFAULT '[]',
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX notification_workspace ON notification (workspace_id, id);

CREATE TABLE chat_topic (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    title         TEXT NOT NULL,
    card_id       INTEGER REFERENCES card(id) ON DELETE SET NULL,
    archived      INTEGER NOT NULL DEFAULT 0,
    deleted_at    TEXT,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX chat_topic_workspace ON chat_topic (workspace_id, updated_at);

-- The session a provider keeps of a topic: the last message it saw and the memory it was given.
CREATE TABLE chat_session (
    topic_id    INTEGER NOT NULL REFERENCES chat_topic(id) ON DELETE CASCADE,
    provider    TEXT NOT NULL,
    session_id  TEXT NOT NULL,
    seen        INTEGER NOT NULL DEFAULT 0,
    memory      TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (topic_id, provider)
);

-- process_id is the reply that took an owner's message, 0 while none has; it outlives the process.
CREATE TABLE chat_message (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    topic_id    INTEGER NOT NULL REFERENCES chat_topic(id) ON DELETE CASCADE,
    role        TEXT NOT NULL,
    text        TEXT NOT NULL,
    context     TEXT NOT NULL DEFAULT '',
    process_id  INTEGER NOT NULL DEFAULT 0,
    retry_key   TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX chat_message_topic ON chat_message (topic_id, id);
CREATE INDEX chat_message_process ON chat_message (process_id);

CREATE TABLE push_subscription (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id  INTEGER NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    device        TEXT NOT NULL,
    endpoint      TEXT NOT NULL UNIQUE,
    p256dh        TEXT NOT NULL,
    auth          TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
