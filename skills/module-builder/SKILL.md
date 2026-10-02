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
`command_try`, `process_get`, `process_log`, `records_list`, `records_upsert`, `cards_list`, `files_list`,
`projects_list`, `triggers_list`, `providers_list`, `health_list`, `asks_list`. Some commands only the owner runs:
`secret_set`, `oauth_start`, `browser_login`, `provider_set`, `provider_add`, `project_enable`; say which one and
why when the module needs it. The owner does them in two places. The Settings of the project (Settings →
Projects → "Settings" on its row): "Enable" with the rights enabling grants, autonomy, pause, the provider of
each command, folders, the secrets its tools take. deck's Settings tabs: Projects (create, import, a trial run),
Providers (add one), Secrets (name and a hidden value), Tokens, Health; sign-ins wait in the cockpit. So name
the place and the value ("Settings → Secrets: `mail-key`, the API key of …"), never a `curl`.

## 1. Interview

A draft sends nothing out until the owner enables it, so build first and ask only what a sensible default
cannot settle: what leaves deck, what costs money, and choices that change the shape of the module. Everything
else takes a sensible default, and the hand-over lists every choice you made yourself. Ask one question at a
time with your question tool; offer 2–4 options, the first one your recommendation. What to settle:

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
  fields that must never change into `immutable`. A later change of the schema or the key that old records do
  not fit needs a `migration` script in the same revision (JSONL of records in, JSONL out); deck runs it on a
  copy and refuses the change with the first problem records otherwise. `retention_days` deletes records (with
  their documents and files) that many days after they were created, `file_retention_days` deletes files of
  records that many days after the file was added. `key`, `display`, `where` and `bind` are expr-lang
  expressions: a field named like one of its functions (`date`, `now`, `count`, `len`, `sum`, `max`, …) reads as
  the function there, so name such fields `day`, `ops` and so on.
  `records_list` returns every record of a collection (the newest `limit` when given; an agent passes one), and
  `record_update` replaces the whole record: work
  over hundreds of records (filtering, batching, merging new fields into old ones) belongs in a script, and an
  agent gets only the slice it judges, from the script's result or its input.
- **Commands** are the verbs. Each has a class: `internal` (records, cards, documents), `reversible`,
  `external` (leaves deck). Choose the executor:
  - `script` for mechanics that repeat the same way (fetch, parse, poll, convert, index) — cheap and exact;
  - `agent` for judgement (rate, summarise, write) — give it a prompt file, skills and a result schema; an
    agent that reads what strangers wrote (mail, pages, feeds) gets `"calls"`, the list of commands it may
    call (`["deck.records_list", "<project>.score"]`, or every command of a project as `"<project>.*"`), so a
    page cannot steer it into the rest;
  - `tool` for exactly one call of a tool: `{"type": "tool", "tool": "<http tool>"}`; the tool gets the
    command input as it is (declare the same input), and the process result is what the tool answered.
  A script that cannot do something reliably becomes an agent, not a bigger script. `timeout_minutes` raises
  the ceiling of a slow command (agents 60, scripts 10 by default).
- **HTTP tools** (`tools.http`) are how the module reaches a service's API: method, `url` with `{field}` from the
  input (path or query, never the host), `headers` with `$secret.<name>` (the owner stores it with
  `secret_set`; scripts and agents never see it, and deck cuts it out of what the service answers), `body`
  template, `input` and `result` schemas. A tool without `class` is `external`. An external call outside
  `allowed_external` waits for the owner; after yes deck makes that exact call and hands the result back. Give
  a paid or risky tool `confirm_ttl_minutes`. A binary answer (video, audio, picture) becomes a file in the
  caller's results folder: `{"file", "type", "size"}`. Everything deck calls outside is journaled; a call whose
  answer was lost is never repeated by deck — the owner retries it or counts it done.
  A tool may sign in with `"auth": {"type": "secret", "secret": "<name>"}` (an `Authorization: Bearer` header;
  `header` and `prefix` change it, e.g. `"header": "X-Api-Key", "prefix": ""`) or oauth2 (`auth_url`, `token_url`,
  secret names `client_id`, `client_secret`, `token`, `scopes`): the owner signs in once with
  `oauth_start`, deck refreshes the token and asks the owner again when the access is revoked. A body of
  `{"file": "{field}"}` sends a file as it is, `{"multipart": {"<part>": "{field}" or "text {field}"}}` a form;
  such a field has `"format": "file"` and takes a file id or `results/<name>` from the caller's own results
  folder, so a script can hand a file it just made to a tool at once. A service that uploads in parts gets
  one HTTP tool per step from its API docs.
- **MCP servers** (`tools.mcp.servers`, the usual `mcp.servers` shape): `command`/`args`/`env` (values may use
  `$secret.<name>`) or `url`; `toolFilter` lists the tools the module uses, `classes` their classes — every
  other tool is `external`. Tools are `<server>.<tool>` in `executor.tool` and `allowed_external`. deck starts
  servers itself, only in an enabled project; a new or changed server waits for the owner's button, and a tool
  whose description or input changes loses its rights until the owner allows it again — so pin the server's
  version in `args` (`"pkg@1.2.3"`, not `@latest`). Once a project runs, `health_list` shows whether each server
  started and why not.
- **Browser**: agents get deck's browser (`browser_navigate`, `browser_snapshot`, `browser_click`, …) with its own
  profile. `domains.read` are the sites it reads, `domains.act` the ones where it clicks, types and submits
  without asking; an action anywhere else waits for the owner, and their "allow always" adds that site to
  `domains.act`. `"*"` in read reads any site (research; it is also the network of scripts), but in a browser
  without the owner's sign-ins: a module that needs a signed-in site lists its sites instead. Reading needs no
  clicks: open the next page by its URL (`?page=2`, `&start=25`) rather than clicking "next", which on a site
  only in `read` waits for the owner each time. The owner signs in to a site once (`browser_login`); a page
  that asks for a sign-in again means the agent calls `browser_needs_login` and stops — deck resumes it after
  the owner signs in.
- **Command input** is a flat object; a choice is `{"type": "string", "enum": [...]}` — an `enum` without
  `"type": "string"` is refused.
- **Files of the input**: a command field with `"format": "file"` takes a file id (from a form upload, Share, or
  `files_list` of a record or card); scripts and agents get the path of a read-only copy. For a trial, upload
  a sample with `file_upload` (the file in base64) and pass the id it returns. A result may give such
  a file to a record or card: `files: [{"file": <id>, "record": <id>}]`.
- **Documents to PDF** (a CV in Typst, say): a script with `programs: ["typst"]` writes the `.typ` documents it
  reads with `document_get` into its work folder, runs `typst compile` and puts the PDF into `$DECK_RESULTS`.
  To compare two revisions of a document, read both (`document_get` with `revision`); screens show the diff.
- **Reads**: `reads: [{"project": "other", "collections": [...], "documents": [...]}]` lets agents and scripts
  read those (`records_list`, `document_get` with `project`) and screens show them (`source.project`).
  Nothing ever writes to another project.
- **Results** are JSON by schema. Decisions use typed shapes: a choice with probabilities and confidence,
  a score 2–10 with confidence, yes/no with a probability. A result of `null` means "nothing to do".
- **Triggers**: `cron` for schedules, `lane` when a card enters a lane, `after` when another command of the
  project finishes with a result that is not `null` (a poll with nothing new returns `null` and the chain stops). The command a trigger starts gets the trigger's fixed `params` plus `{"card": <id>}` for
  `lane` or `{"process": <id>}` of the finished one for `after`, so its input declares that field as
  `integer`; a later step that needs an earlier result
  reads it with `process_get`, so keep what the chain needs in records rather than passing it along. Cycles are refused. After the Mac slept, each missed schedule runs once; `"skip_missed":
  true` drops the missed run instead (a morning digest that is stale by noon).
- **Board**: lanes have stable names, titles for people and optional roles (`inbox`, `ready`, `running`,
  `review`, `done`), each role on at most one lane. New cards land in `inbox`, at the end of the lane; to keep the best on top, place each with `card_move` and
  `before`. `ready`, `running` and
  `review` are for code runs and come only all three together; a pipeline the owner drives by hand needs
  only `inbox` and maybe `done`. A card can point at a record (`record`). A project can live without a board.
- **Documents** agents and scripts write are artifacts only: each belongs to one card or one record, not
  both. A daily digest or report is an artifact of a record made for it (one record a day); `prompt.md`,
  `prompts/*.md` and other module documents are written by you, not by the module at run time.
- **Screens** are lists of blocks from the catalog (`schemas/catalog.json`; in `manifest_format` each block type
  has its exact fields and its rules in words in `description`, and a refusal names the JSON path and the
  schema rule, such as `missing property 'item'` or `additional properties 'source' not allowed`):
  `table`, `list`, `card` (the record of a record page), `stat`, `actions`, `board`, `form`, `custom`, and
  `columns`/`tabs` to lay them out. A table:
  `{"type": "table", "title": "Best", "source": {"collection": "items", "where": "score >= 7", "sort": "-score",
  "limit": 20}, "columns": [{"label": "Title", "bind": "title"}], "filter": ["status"], "actions": [{"label": "Rate", "command":
  "rate", "params": {"id": "$record.id"}}]}` — `where` and `bind` are expressions over record fields,
  `sort` is a field with `-` for descending, `filter` lists fields the owner narrows by. A column, a `card`
  field and a `stat` take `format`, and the client shows the raw value in the owner's locale and time zone:
  `number`, `percent` (0.42 is 42 %), `money` with `currency` (`{"label": "Price", "bind": "price", "format":
  "money", "currency": "USD"}`), `date`, `datetime`, `relative` (5 min ago), `duration` (seconds), `bytes`,
  `link`, `tag` — so a bind is never `string(round(change * 100)) + '%'`. Action `params` are strings in the manifest; `$record.<field>`
  passes the field's value with its type (`$record.id` is a number, so that input field is an `integer`), and
  a fixed value becomes the type of its input field (`"days": "7"` to an `integer` is 7). An action passes
  every required parameter of its command
  (from `$record.<field>` or fixed values); deck asks for nothing that is missing, so a choice the owner
  makes in a row is one action per option. A button whose call the owner may want back names the call that takes
  it back, `"undo": {"command": "unrate", "params": {"id": "$record.id"}}`, offered for a moment after it went
  through. A command the owner fills in by hand is a `form`:
  `{"type": "form", "command": "score", "save": "items"}` asks for the command's input by its schema, shows the
  result by the result schema and, with `save`, keeps it as records; a field picked from a source takes
  `"choices": {"note": {"source": {"collection": "notes"}, "value": "_id", "label": "title"}}`, and a field with
  `"format": "project"` offers the projects by itself; on a record page
  `{"type": "form", "command": "deck.record_update"}` edits the record, `immutable` fields read only. `board`
  shows the project's board (no source). `{"type": "document", "document": "prompts/criteria.md"}` shows a
  document of the project, on a record page one of the record (an artifact): markdown drawn, an md document
  edited by the owner as a new revision, its revisions and going back to one; every project also gets a
  Documents screen from deck. Long text in a `list` is markdown with `"item": {…, "markdown": true}`, a `card`
  field with `"markdown": true`; everywhere else text stays plain. What the catalog cannot show is a `custom` block: an MCP App — one HTML
  file under `custom/` (`document_save` with kind `custom`), `{"type": "custom", "document": "custom/chart.html",
  "source": …, "actions": […]}`. It runs without network: `import { App } from "/ext-apps.js"`, the items of its
  source and the page's record arrive in `app.ontoolresult` (`structuredContent.items`, `.record`), and
  `app.callServerTool({name: "<project>.<command>", arguments})` calls only the commands of its `actions` (the
  button whose fixed `params` the arguments do not contradict; an external command waits for the owner's yes).
  Its colors, fonts and corners are the host's: `app.getHostContext()` and `app.onhostcontextchanged` give
  `theme` (`light`/`dark`) and `styles.variables` by MCP Apps names (`--color-background-primary`,
  `--color-text-primary`, `--color-text-secondary`, `--color-border-primary`, `--color-ring-primary` for the
  accent, `--font-sans`, `--font-text-sm-size`, `--border-radius-md`); apply them with `applyDocumentTheme` and
  `applyHostStyleVariables` from `/ext-apps.js`, again on each change, and write CSS only with `var(--…)` of
  them — a fixed color breaks in the owner's dark scheme. List screens in `menu`. A record page is a screen with
  `"record": "<collection>"`; a row's button `{"label", "screen": "<that page>"}` opens it with the row's record
  and takes no params.
- **Telling the owner**: a script or agent that finds something worth a look calls `notify` with `text`, and
  optionally the `screen` (and `record`) it opens and `actions` (`[{label, command, params}]`); it becomes a line
  in the cockpit feed. The caller decides it is worth it; a run with nothing new says nothing.
- **Suggesting work**: a script or agent that finds work the owner may want — a card for this or another
  project, or a call of a command — calls `suggest` with a one-line `reason` instead of doing it; it waits in
  "waiting for you" for yes, no or another project (in a project on autonomy `auto` it is done at once and shows
  in "done without you"). It reads `suggestions_rejected` first and does not suggest again what the owner turned
  down. A card in another project is the only way a module writes there.
- **Remembering what was seen**: `records_upsert` replaces a whole record by its key, so write every
  required field. `record_get` reads one record by its id (a card's `record`); `records_list` gives the newest. `retention_days` deletes records with their documents: a collection that remembers what the
  owner has already seen, or holds what they asked for, gets none.
- **Starting records** (the first sources, say) are written after the import with `records_upsert`, or by the
  first run of the script that needs them.

## 3. Typical techniques

These live here, not in deck: deck stays small and you combine them.

- **Incoming events** (new mail, new postings, new files): a script on a schedule polls the source from a
  cursor kept in a record, writes only new items, and an agent command runs only on what is new
  (`after` trigger, or the script's result lists the new ids). After the machine slept, the next poll takes
  everything since the cursor — never "the last N hours". The first run has no cursor: take a window the owner
  agrees to (the last two weeks, the current thread) and name it in the hand-over.
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
- no secrets and no keys of the owner; ask for an HTTP tool of the manifest instead. Never ask the owner to
  `export` a key for a module: on macOS a confined program reads the environment of the owner's processes, so
  keys live only in deck secrets;
- `runtime` is `python`, `node`, `bash` or `exec` (a program that runs by itself); `timeout_minutes` as for
  commands;
- a script runs alone: its folder holds only its own file, so a helper shared by scripts is repeated in each
  (or read with `document_get` into the work folder);
- deck commands over HTTP: `curl -s -H "Authorization: Bearer $DECK_TOKEN" -d '<json>' "$DECK_URL/commands/deck.records_upsert"`
  (`GET $DECK_URL/commands` lists them, with the `result` schema where a command has one; works through the proxy or without it).
  A 200 body is the command's result itself, with no wrapper; an error is plain text with a 4xx/5xx status, 410
  when the process has finished; `records_upsert` takes JSONL
  and returns `{"written", "ids", "errors"}`; `records_list` returns `[{"id", "key", "title", "data": {…}}]`,
  newest last, `process_get` `{"id", "command", "state", "result", "error", …}`, `documents_list`
  `[{"id", "name", "kind", "owner_kind", "owner_id"}]` (an artifact of a record has `owner_kind` `record`) and
  `card_create` the card with its `id`; a script may call commands and tools of its own project that
  allow `agent` callers; a call that waits for the owner answers HTTP 202 `{"waiting": <ask>}` — deck makes
  it later, the script goes on;
- files go into `$DECK_RESULTS`; the result field `files: [{"path": "...", "record": <id>}]` attaches them;
- a script installs its own packages when it runs; there is no `packages` field. The registries go into
  `domains.read` (Python: `pypi.org`, `files.pythonhosted.org`; Node: `registry.npmjs.org`), the package
  manager goes through the proxy, its cache lives in `$DECK_DATA`, the install counts in `timeout_minutes`;
  a project without the registries in `domains.read` has no packages. Programs it needs from the machine go
  into `scripts[].programs` (`uv`, `npm`). Python: `"runtime": "exec"` and a PEP 723 block:
  ```python
  #!/usr/bin/env -S uv run --script
  # /// script
  # dependencies = ["httpx==0.28.1"]
  # ///
  ```
  Node: `npm install` into `$DECK_DATA`, output to stderr (stdout is the result), then `createRequire`:
  ```js
  const dir = process.env.DECK_DATA + "/node";
  require("node:child_process").execFileSync("npm", ["install", "--prefix", dir, "--no-audit", "--no-fund", "left-pad@1.3.0"], { stdio: ["ignore", 2, 2] });
  const leftPad = require("node:module").createRequire(dir + "/")("left-pad");
  ```

## 5. Agent commands

The prompt says what to do with the input and what the result means; facts and criteria live in md
documents it reads, and a skill carries a reusable way of working. The agent reaches deck only through the
deck MCP server. A provider with a shell (Claude Code, Codex, an ACP agent) runs it in its auto mode: the agent works in its own
folder and in the project's data folder (the prompt names it; indexes, caches and state live there between
runs), runs shell commands, and searches and fetches the web without asking; the provider itself refuses
what looks risky and asks the owner only about that. Secrets, deck's own files and other projects stay closed
to it. A deck tool of class `external` outside `allowed_external` still waits for the owner. Set
`"no_network": true` for a project with personal data (finance, health, mail): its agents get no web and no
network of their own and reach outside only through deck tools; removing the flag later is a right. An agent
that must work in a folder of the owner (a code repo, notes) gets `"folder": true` in `executor` (or the folder's
name when the project has several): it works there instead of its own folder, with the repo's CLAUDE.md or
AGENTS.md, and a second agent in the same folder waits. Only the owner adds folders (the Settings of the
project → "Add folder"); a code repo is the code-project skill. List the
skills it needs in `executor.skills`; a skill may not grant tools (`allowed-tools`, `hooks`) — rights come
from deck.

The owner picks the model provider of each agent command in settings (`provider_set`); the manifest only says
what the command needs and suggests one. `requires` lists capabilities, and usually only two matter:
`no_cloud` for personal data (finance, health) that must stay on the owner's hardware, and `sandbox` when the
agent must run shell commands or write files (a provider in auto mode, or a model the owner runs in an ACP agent
inside deck's sandbox). The two go together:
a local model in an ACP agent has a shell and stays on the owner's hardware. `web` decides only which provider may run the command: an agent on Claude Code has the web
whether or not it is required. Require it when the agent's result is wrong without its own research (reading
changelogs or news it cannot know in advance), so the owner cannot move it to a local model that has none;
leave it out when a script brings the pages the agent judges, which is cheaper and keeps local models
possible. Fetching known pages belongs in a script with `domains.read`. Repeatable mechanics still belong in a script: it is cheaper,
runs the same way every time and works with any provider. Do not require `tools` or `structured_output`: every provider has deck's tools and hands in the result, and a
requirement a local model lacks keeps routine work off it. `providers_list` with the command shows which
providers fit it. `provider` suggests a provider by
name — routine judgement suits a local model in an ACP agent (the name the owner gave it, from
`providers_list`), heavy reasoning Claude Code or Codex. Write the
prompt so it works on a small model too: say which deck tool to call for what, and that the result is handed in
at the end; a provider without output by schema gets the `result_submit` tool for it. A small model copies
examples from the prompt word for word and misses checks that are plain in the text: give it the shape of the
result without sample values, let the script do every check a pattern can do and pass the findings in the
input, and let the script, not the model's verdict, decide what reaches the owner. An agent that judges many
items hands in all its verdicts in its result and an `after` script writes them into the records: a small
model that reads and rewrites records one by one drops fields and runs out of time. Text in a language other
than English is where a small model slips most (mixed languages, stray alphabets): let the script check it and
try the command on the model it will run on before the hand-over.

## 6. Confirmations

Each "yes" the owner gives by habit is a failure of the module. What an agent does in its own folders, in its
shell and on the web does not wait for the owner: the provider's auto mode passes it and asks only about what
it finds risky, which should be rare in a normal run. What waits is deck tools of class `external` outside
`allowed_external`, reversible actions in the `suggest` mode, and questions the agent asks. Before hand-over,
count what will wait for the owner in a normal week and remove what they would always allow:

- a tool that only reads (search, fetch, status) gets `"class": "internal"`; `external` is for what changes
  something outside or costs money;
- something the owner will look at before it takes effect anyway (a draft, an unpublished post, a job started
  in a paid service they asked for) goes into `allowed_external`, and the hand-over says so;
- sites where the agent must click, type or submit on its own go into `domains.act`; everywhere else it reads;
- a paid or risky call that should still be asked every time keeps `external` and gets `confirm_ttl_minutes`.

Name each of these rights in the hand-over: the owner grants them with "enable".

## 7. Build, import, try

1. Write the module as a folder: `manifest.json`, `prompt.md` (its first line is the one-line description
   deck and the master choose the project by), `prompts/*.md`, `scripts/*`, `skills/<name>/SKILL.md`,
   other md documents by their names. Only `prompt.md` has kind `prompt`; `prompts/*.md` and the other md
   come in as `doc`, so later `document_save` them with kind `doc`. Keep test data outside the module folder.
2. `project_import` with the absolute folder path creates a **draft**. A refused import comes back with the
   reason and leaves nothing behind; fix the folder and import again. Once imported, change the draft with
   `manifest_save` and `document_save` (a second import of the same name is refused), and keep the folder
   in step.
3. `command_try` runs a command as a trial: everything that is not internal is declined, so nothing leaves
   deck. It runs as you, the master: leave `callers` out (everyone) unless a command must be closed to
   someone, and keep `master` in a list you give. The `after` and `lane` triggers it sets off run as trials too, so a whole chain can be tried from
   its first command. A trial is not a sandbox: internal writes (records, cards, documents) are real and stay
   in the draft, and so is what scripts write to `$DECK_DATA`, which you cannot reach: keep what a module
   remembers (seen items, cursors) in records when trials must be undone. Before the hand-over, delete what
   the trials left that the owner should not start with: `record_delete` (in a draft only), `card_delete`;
   say in the hand-over what you kept; lines a trial sent with `notify` stay in the feed. `process_get` gives
   the state and result. A `lane` step is tried with `command_try` of its command and `{"card": <id>}`: in a
   draft moving the card starts nothing; `process_log` shows the events of a process, also after it ended,
   and a local model may think for many minutes before its first one: wait with
   `process_get` in the foreground. Time the trial on the provider the command will run on and set
   `timeout_minutes`, batch sizes and the schedule by it. A trial that ends `done` is not yet a proof: check
   that the chain gave what the owner will see (records, cards, documents). Check `records_list` and `cards_list`, and `health_list` after a save that adds programs or
   MCP servers: a missing program or a failed server shows there with its error.
4. A draft never runs on its schedule or on the owner's card moves. Only the owner enables it.

## 8. Moving modules and skills

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

## 9. Rights

Ask only for what the module needs. Autonomy `auto`, `allowed_external`, domains, secrets, milder classes of
tools, `reads` of other projects and removing `no_network` are rights: in a draft they are part of what the owner enables; in an
enabled project a change that widens them waits for the owner's button. Every other change you make to an
enabled project is listed as "done without you" with an undo.

## 10. Hand over

Tell the owner in a few plain sentences: what the module does and when, what the trial showed, and the
rights "enable" will grant. Do not enable it yourself.

## 11. Improve this skill

When the owner fixes by hand a module you built, or you had to guess because this skill was silent or wrong,
fix the section that led you there: `document_save`, kind `skill`, name `skills/module-builder/SKILL.md`, no
project. Change the rule itself, in the words of the section, and keep the skill general; no dated notes,
examples of one module or history — deck keeps the revisions. Say in the hand-over what you changed.
