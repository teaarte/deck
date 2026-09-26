---
name: module-builder
description: Builds a deck module (a project with a manifest, prompt, scripts, skills and screens) from an interview with the owner, imports it as a draft and proves it with trial runs. Use when the owner wants deck to take over a new kind of routine work.
---

# Building a deck module

deck is the owner's control panel for digital workers. A new line of work is a **project**: a JSON manifest
plus documents (md prompt, scripts, skills). deck gives models channels, tools, data, limits and screens; the
work itself is done by scripts and agents you describe. You build the module; the owner answers questions
and presses "enable".

You talk to deck through its MCP server (`deck`). The tools are deck commands: `manifest_format`,
`project_import`, `documents_list`, `manifest_get`, `manifest_save`, `document_get`, `document_save`,
`command_try`, `process_get`, `process_log`, `records_list`, `cards_list`, `projects_list`.

## 1. Interview

Ask one question at a time with your question tool; offer 2–4 options, the first one your recommendation.
Stop asking when you could write the manifest without guessing. Cover:

- the outcome: what the owner wants to see each day or week, and what "done" means for one item;
- the inputs: where items come from, how often they change, whether a login is needed;
- judgement vs mechanics: which steps need taste (an agent) and which are repeatable (a script);
- the board: whether items need to move through stages the owner acts on, and which stages;
- anything that leaves deck (posting, sending, paying, submitting): what exactly and how often;
- criteria and voice: facts, rules and examples that belong in md documents, not in the manifest.

When the owner gave written answers instead, work from them and list every choice you made yourself in the
hand-over.

## 2. Design

Read `manifest_format` first: it is the JSON Schema deck checks every manifest against.

- **Collections** hold the data. Pick a key that identifies an item for good (a URL, an external id); records
  with the same key are updated, not duplicated. Give `display` so people and voice can name a record. Put
  fields that must never change into `immutable`. `records_list` returns the newest records up to a limit, and
  `record_update` replaces the whole record: work over hundreds of records (filtering, batching, merging new
  fields into old ones) belongs in a script, and an agent gets only the slice it judges, from the script's
  result or its input.
- **Commands** are the verbs. Each has a class: `internal` (records, cards, documents), `reversible`,
  `external` (leaves deck). Choose the executor:
  - `script` for mechanics that repeat the same way (fetch, parse, poll, convert, index) — cheap and exact;
  - `agent` for judgement (rate, summarise, write) — give it a prompt file, skills and a result schema;
  - `tool` for exactly one call of a tool: `{"type": "tool", "tool": "<http tool>"}`; the tool gets the
    command input as it is (declare the same input), and the process result is what the tool answered.
  A script that cannot do something reliably becomes an agent, not a bigger script.
- **HTTP tools** (`tools.http`) are how the module reaches a service's API: method, `url` with `{field}` from the
  input (path or query, never the host), `headers` with `$secret.<name>` (the owner stores it with
  `secret_set`; scripts and agents never see it), `body` template, `input` and `result` schemas. A tool
  without `class` is `external`. An external call outside `allowed_external` waits for the owner; after yes
  deck makes that exact call and hands the result back. Give a paid or risky tool `confirm_ttl_minutes`. A
  binary answer (video, audio, picture) becomes a file in the caller's results folder: `{"file", "type",
  "size"}`. Everything deck calls outside is journaled; a call whose answer was lost is never repeated by
  deck — the owner retries it or counts it done.
- **Reads**: `reads: [{"project": "other", "collections": [...], "documents": [...]}]` lets agents and scripts
  read those (`records_list`, `document_get` with `project`) and screens show them (`source.project`).
  Nothing ever writes to another project.
- **Results** are JSON by schema. Decisions use typed shapes: a choice with probabilities and confidence,
  a score 2–10 with confidence, yes/no with a probability. A result of `null` means "nothing to do".
- **Triggers**: `cron` for schedules, `lane` when a card enters a lane, `after` when another command of the
  project finishes. Cycles are refused.
- **Board**: lanes have stable names, titles for people and optional roles (`inbox`, `ready`, `running`,
  `review`, `done`), each role on at most one lane. New cards land in `inbox`. `ready`, `running` and
  `review` are for code runs and come only all three together; a pipeline the owner drives by hand needs
  only `inbox` and maybe `done`. A card can point at a record (`record`). A project can live without a board.
- **Documents** an agent writes (artifacts) belong to one card or one record, not both.
- **Screens** are lists of blocks. Today deck draws `table` and `actions`:
  `{"type": "table", "title": "Best", "source": {"collection": "items", "where": "score >= 7", "sort": "-score",
  "limit": 20}, "columns": [{"label": "Title", "bind": "title"}], "actions": [{"label": "Rate", "command":
  "rate", "params": {"id": "$record.id"}}]}` — `where` and `bind` are expressions over record fields,
  `sort` is a field with `-` for descending. List screens in `menu`.
- **Starting records** (the first sources, say) are written after the import with `records_upsert`, or by the
  first run of the script that needs them.

## 3. Typical techniques

These live here, not in deck: deck stays small and you combine them.

- **Incoming events** (new mail, new postings, new files): a script on a schedule polls the source from a
  cursor kept in a record, writes only new items, and an agent command runs only on what is new
  (`after` trigger, or the script's result lists the new ids). After the machine slept, the next poll takes
  everything since the cursor — never "the last N hours".
- **Search over big data**: a script builds an index (SQLite, a vector file, anything) in `$DECK_DATA`, the
  project's own data folder that survives runs and goes into snapshots; a `search` command reads it.
- **Reacting faster than a minute, or being always up**: deck sleeps with the owner's Mac. Such work belongs
  to an external service; deck starts it, polls it by a script, and keeps its state in records.
- **External jobs** (generation in a paid service): one command starts the job and stores its id in a
  record with status `waiting`; a script on a schedule checks the waiting records and downloads the files.
- **Process marker**: anything created outside deck (a draft, a job, a post) carries the id of the deck
  process that created it, so after "outcome unknown" a search finds it instead of creating a duplicate.

## 4. Scripts

A script reads its input JSON from stdin and prints its result JSON to stdout. It runs in a sandbox:

- write access only to its run folder and `$DECK_DATA`; the project folders are read-only;
- network only to the manifest's `domains.read`, through the proxy deck sets in `HTTP(S)_PROXY`;
- no secrets and no keys of the owner; ask for an HTTP tool of the manifest instead;
- deck commands over HTTP: `curl -s -H "Authorization: Bearer $DECK_TOKEN" -d '<json>' "$DECK_URL/commands/deck.records_upsert"`
  (`GET $DECK_URL/commands` lists them; works through the proxy or without it); `records_upsert` takes JSONL
  and returns `{"written", "ids", "errors"}`; a script may call commands and tools of its own project that
  allow `agent` callers; a call that waits for the owner answers HTTP 202 `{"waiting": <ask>}` — deck makes
  it later, the script goes on;
- files go into `$DECK_RESULTS`; the result field `files: [{"path": "...", "record": <id>}]` attaches them;
- packages go into `scripts[].packages` (Python through `uv`, Node through `npm`), programs it needs from
  the machine into `scripts[].programs`; nothing is installed while the script runs.

## 5. Agent commands

The prompt says what to do with the input and what the result means; facts and criteria live in md
documents it reads, and a skill carries a reusable way of working. The agent reaches deck only through the
deck MCP server; besides it, it can read its own work folder (`Read`, `Glob`, `Grep`) and use its skills; its
web tools are off. Anything external it wants waits for the owner's confirmation. List the skills it needs in
`executor.skills`; a skill may not grant tools (`allowed-tools`, `hooks`) — rights come from deck.

## 6. Build, import, try

1. Write the module as a folder: `manifest.json`, `prompt.md` (its first line is the one-line description
   deck and the master choose the project by), `prompts/*.md`, `scripts/*`, `skills/<name>/SKILL.md`,
   other md documents by their names.
2. `project_import` with the absolute folder path creates a **draft**. A refused import comes back with the
   reason and leaves nothing behind; fix the folder and import again. Once imported, change the draft with
   `manifest_save` and `document_save` (a second import of the same name is refused), and keep the folder
   in step.
3. `command_try` runs a command as a trial: everything that is not internal is declined, so nothing leaves
   deck. The `after` and `lane` triggers it sets off run as trials too, so a whole chain can be tried from
   its first command. `process_get` gives the state and result; `process_log` shows events only while the
   process is open. Check `records_list` and `cards_list`.
4. A draft never runs on its schedule or on the owner's card moves. Only the owner enables it.

## 7. Moving modules and skills

- **Export a module** into a new or empty folder: `documents_list` of the project, then `document_get` of every
  document of kind manifest, prompt, script, skill, custom and doc that has no `owner_kind`, written to the
  folder under its name. Artifacts and documents of records and cards are data, not the module; leave them
  out. `project_import` of that folder gives the module back as a draft; kinds come from the path — scripts
  listed in `scripts[].file`, `skills/…`, `custom/…`, `manifest.json`, `prompt.md` — and anything else comes
  back as `doc`, so keep scripts listed and custom blocks under `custom/`.
- **Import the owner's Claude Code skills**: for each folder `~/.claude/skills/<name>/`, save `SKILL.md` first
  with `document_save`, kind `skill`, name `skills/<name>/SKILL.md`, no project — a skill of the whole
  workspace (with `project`, of one project); only if deck took it, save the other text files as
  `skills/<name>/<path in the folder>`. Skip a file whose text equals what `document_get` already returns, so
  history does not grow. Leave out hidden files, binaries and files over 1 MB. `SKILL.md` needs
  `name: <name>` and a `description`; deck refuses skills that grant tools (`allowed-tools`, `hooks`) and path
  parts that do not start with a letter or digit (`__init__.py`) — tell the owner what did not come in and why.

## 8. Rights

Ask only for what the module needs. Autonomy `auto`, `allowed_external`, domains, secrets, milder classes of
tools and `reads` of other projects are rights: in a draft they are part of what the owner enables; in an
enabled project a change that widens them waits for the owner's button. Every other change you make to an
enabled project is listed as "done without you" with an undo.

## 9. Hand over

Tell the owner in a few plain sentences: what the module does and when, what the trial showed, and the
rights "enable" will grant. Do not enable it yourself.

## Lessons

Every manual fix the owner makes to a module built with this skill becomes a line here.
