---
name: module-builder
description: Builds a deck module (a project with a manifest, prompt, scripts, skills and screens) from an interview with the owner, imports it as a draft and proves it with trial runs. Use when the owner wants deck to take over a new kind of routine work.
---

# Building a deck module

deck is the owner's control panel for digital workers. A new line of work is a **project**: a JSON manifest
plus documents (md prompt, scripts, skills). deck gives models channels, tools, data, limits and screens; the
work itself is done by scripts and agents you describe. You build the module; the owner answers questions
and presses "enable". Its scripts, prompts and their review follow the module-conventions skill.

You talk to deck through its MCP server (`deck`). The tools are deck commands: `manifest_format_get`,
`project_import`, `documents_list`, `manifest_get`, `manifest_save`, `document_get`, `document_save`,
`command_try`, `process_get`, `process_log_get`, `records_list`, `record_upsert`, `files_list`,
`projects_list`, `triggers_list`, `models_list`, `health_list`, `asks_list`, `command_describe`. Ask a read for
the fields you need (`fields`; of `records_list` the fields of the data of each record). Some commands the owner
alone runs: `secret_set`, `oauth_start`, `browser_login`, `project_enable`; say which one and why. `commands_list`
marks such a command `owner_only` with `form`, the address of its form, and `form_params` that fill it: give the
owner that link with the values (the secret `mail-key`, …), never a `curl`. `model_set` and `assignment_set` a
module or the master calls as a proposal: the call waits for the owner's button and applies exactly what it sent;
several assignments go in one call (`assignments`, a JSON list of `{role, project, command, models or model}`);
give the owner the `link` of the answer, which opens that request.

When the module needs something the owner gives — an API key, an account or sign-in, a right, a program
installed on the machine, a paid call in a trial — stop and ask the owner in the chat: what, why, and where to put
it. Do not work around it: no other service or free key without the owner's word, no right declared just to get
past a refusal, no key in a document, script or the environment, no copy by hand of what deck or its sandbox
refuses. Build the rest, then go on with that part once the owner has done it.

## 1. Interview

A draft sends nothing out until the owner enables it, so build first and ask only what a sensible default
cannot settle: what leaves deck, what costs money, and choices that change the shape of the module. Ask one
question at a time with your question tool, 2–4 options, the first your recommendation. Settle:

- the outcome: what the owner wants to see each day or week, and what "done" means for one item;
- the inputs: where items come from, how often they change, whether a login is needed;
- judgement vs mechanics: which steps need taste (an agent) and which repeat (a script);
- stages the owner acts on (a `status` field of the record), and anything that leaves deck (posting, sending,
  paying): what exactly and how often;
- criteria and voice: facts, rules and examples that belong in md documents, not in the manifest.

With written answers instead, work from them. The hand-over lists every choice you made yourself.

## 2. Design

Read `manifest_format_get` first: it is the JSON Schema deck checks every manifest against, and the
`description` of each field and block says its rules. This section says what the schema does not: which piece
to pick and how they fit.

- **Collections** hold the data. Pick a key that identifies an item for good (a URL, an external id): a record
  with the same key is updated, not duplicated. Give `display`; put fields that never change into `immutable`.
  A later change of the schema or key that old records do not fit needs a `migration` script in the same
  revision (JSONL in, JSONL out). What belongs to a record goes with it when it is deleted: its `folder` in the
  data folder (`"'videos/' + slug"`, or one file), and the records of a collection that names it as `parent`.
  Lay out the data folder by record: `<collection>/<key>/` for what a record has, `cache/` for what can be made
  again, nothing loose at its root. `key`, `display`, `where` and `bind` are bare expr-lang expressions (`"key":
  "url"`, no `=`; the `=` begins a value of `params`, `values` or `input`): a field named
  like one of its functions (`date`, `now`, `count`, `len`, `sum`, `max`, …) reads as the function, so name such
  fields `day`, `ops`. `records_list` returns all records (the newest `limit` when given; an agent passes one)
  and `record_update` replaces the whole record: work over hundreds of records belongs in a script, and an agent
  gets the slice it judges.
- **Commands** are the verbs. One that acts in the owner's name outside deck declares `send` on itself
  (section 10) and may name the command that takes it back in `undo`. The executor:
  - `script` for mechanics that repeat (fetch, parse, poll, convert, index) — cheap and exact;
  - `agent` for judgement (rate, summarise, write) — a prompt file, skills and a result schema; an agent that
    reads what strangers wrote (mail, pages, feeds) gets `"calls"`, the commands it may call
    (`["deck.records_list", "<project>.score"]` or `"<project>.*"`), so a page cannot steer it into the rest;
  - `tool` for one call of a tool (`{"type": "tool", "tool": "<http tool>"}`, the same input as the tool);
  - `model` for one call of a model without an agent (`{"type": "model", "kind": "text", "prompt_file": …}`):
    `text` answers by the result schema, `image` draws (or edits the picture of its one file field), `tts`
    speaks the field `text`, `stt` writes down its one file field, `music` makes a track, `video` a clip from the
    file fields `first_frame` and `last_frame` when given. A picture, track, speech or clip is a file of the
    result. The model is the one the owner assigned to the kind; `"model"` names another, `"options"` adds fields
    of the request (`voice`, `image_config`).
  A script that cannot do something reliably becomes an agent, not a bigger script. `timeout_minutes` raises the
  ceiling of a slow command (agents 60, scripts 10 by default). A command runs one at a time
  (`"runs": "one"`): a call while a run goes on waits for it, so runs that write the same records, files, clone or
  browser never break each other, and the module writes no lock of its own. `"runs": "many"` only for a command
  whose runs write nothing in common, or that calls itself. Scripts of a module whose folders are on a node work on
  a copy of `$DECK_DATA` there, and a file deck changed meanwhile stays deck's: keep what two commands write apart.
- **HTTP tools** (`tools.http`) reach a service's API: `url` with `{field}` from the input (never the host),
  `headers` with `$secret.<name>` (a secret of this module the owner stores with `secret_set`; deck cuts it out of
  answers), `body` template, `input` and `result` schemas, `auth` by a secret or oauth2 (the owner signs in once
  with `oauth_start`). deck reads the right a tool needs (section 10): GET or HEAD without a key is `network` on its
  host, with a key `data` on the tool, any other method `send` on the tool; `"right"` names another (`data` for a
  POST that only searches). A private link whose token is the URL (a published calendar, a webhook) is a tool with
  `"url": "$secret.<name>"` and `"hosts"`, the hosts it may point to. A binary answer becomes a file in the caller's
  results: `{"file", "type", "size"}`; a body `{"file": "{field}"}` or `{"multipart": {…}}` sends a file of a
  `"format": "file"` field. A field of `"format": "json"` alone in a JSON body puts the value it holds. deck journals
  every call outside; a call whose answer was lost is not repeated by deck — the owner retries it or counts it done.
- **Models** are deck's, declared once in its settings (connections such as OpenRouter or Ollama, their models
  with prices, and the models assigned to text, image, tts, stt, music, video, with fallbacks). The module calls
  them through deck, never with a key: a command of type `model`, `deck.model_call` from a script or agent, or an
  API path through `$DECK_MODELS` (section 5). Every call names a model of deck's settings or an `@<kind>`. The
  module declares what it calls under `money` (section 10). A model the module needs and deck lacks it proposes
  with `deck.model_set` (connection, model, kind); the call waits for the owner's button. A free model it added
  that no assignment names any more it removes with `deck.model_delete`, declared under `send`.
- **MCP servers** (`tools.mcp.servers`, the usual `mcp.servers` shape): `command`/`args`/`env` (values may use
  `$secret.<name>`) or `url`; a `command` server reaches the web through deck's proxy, to the hosts of `network`.
  `toolFilter` lists the tools the module uses, `"right"` the right of each (`network` for one that reads what is
  public, `data`), `send` for the rest. Tools are `<server>.<tool>` in `executor.tool` and in `rights`. A tool whose
  description or input changes asks the owner again, so pin the version in `args` (`"pkg@1.2.3"`). `health_list`
  shows whether each server started and why not.
- **Browser**: agents get deck's browser (`browser_navigate`, `browser_snapshot`, `browser_click`, …). It reads the
  sites of `network` and acts on those of `send` (a site by its host); an action elsewhere waits for the owner.
  `"*"` reads any site, without the owner's sign-ins. Open the next page by its URL (`?page=2`) rather than
  clicking "next", which waits each time. The owner signs in to a site once (`browser_login`); a page that asks for
  a sign-in again means the agent calls `browser_login_request` and stops — deck resumes it after.
- **Command input** is a flat object; a choice is `{"type": "string", "enum": [...]}`. A bounded number has
  `minimum` and `maximum` (a slider). A field with `"format": "file"` takes a file id (an upload, Share, or
  `files_list` of a record); scripts and agents get the path of a read-only copy named `<file id>-<original name>`.
  For a trial, upload a sample with `file_upload` and pass the id. A result gives such a file to a record with
  `files: [{"file": <id>, "record": <id>}]`.
- **Data of other modules**: `data` on `other` (all of it), `other.items` (a collection) or `other/notes/a.md` lets
  agents and scripts read those (`records_list`, `document_get` with `project`), and a screen shows them with
  `"source": {"from": "deck.records_list", "input": {"project": "other", "collection": "items"}}`. `*:models` lets
  them read which models do the work of all modules (`models_list` with `project`; propose a change with
  `assignment_set`); `*:runs` reads runs in brief (`processes_list` with `project`). Nothing writes to another module.
- **Results** are JSON by schema. Decisions use typed shapes: a choice with probabilities, a score 2–10 with
  confidence, yes/no with a probability. A result of `null` means "nothing to do", allowed where the schema takes it
  (`"type": ["object", "null"]`).
- **Triggers**: `cron` for schedules, `after` when another command of the project finishes with a result that is
  not `null` (a poll with nothing new returns `null` and the chain stops). The started command gets the trigger's
  `params` plus `{"process": <id>}` of the finished one (declare it as `integer`), and a step whose input declares
  `record` gets the record of the finished one, so the steps of a record show on its page. A later step reads an
  earlier result with `process_get`: keep what the chain needs in records. A chain runs at most five steps deep;
  two `after` triggers on one step run side by side, so a step that prepares something can run beside the next.
  After the Mac slept, each missed schedule runs once (`"skip_missed": true` drops it). `{"on": "ask", "command": X}`
  starts X when a confirmation the module asked for closes, with `ask`, `answer` (`yes`, `no`, `expired`) and
  `process` — instead of polling for the answer.
- **Stages and work in progress**: stages are a `status` enum of the record, shown as a board (`"board": "status"`
  on a `list`). Work in progress is a status (`applying`) the command sets first and its script clears: the row
  shows it by `tone`, the button hides by `"when": "status != 'applying'"`, and a button whose second press must not
  start the work twice passes `"retry_key": "='apply:' + string(_id)"`. A call with a `record` while a process of
  the same command works on that record returns that process; an agent gets it as a message in its run.
- **Expressions are JSON text**: a line break is `\\n` in the manifest (`"text": "=title + '\\n' + note"`); a JSON
  `\n` alone puts a raw line break into the quotes, and the expression is refused.
- **Deleting**: derived state (an index, totals) hears nothing when the owner deletes a record: rebuild it from the
  records each run. Do not build a module on deleting: a record that is done keeps a `status` (`archived`) its
  screens leave out; `deck.record_delete` and `deck.file_delete` delete for good what must go, with an undo line
  for the owner; `retention_days` clears old records, so a collection of what the owner has seen gets none.
- **Documents** agents and scripts write are artifacts, each of one record, and the memory of the project; a daily
  report is an artifact of a record made for it. `prompt.md`, `prompts/*.md` and other module documents are yours —
  unless a command's work is to change them: then `"writes"` names them (`"personas/style.md"`, a folder `"remotion/"`),
  never the manifest; each change shows to the owner with an undo, and a command that changes documents without
  `writes` is refused at its first save.
- **Widgets** are the views of the module's card on the tiles: `"widgets": [{"min": "2x1", "blocks": [...]}]`, one
  to four `stat`, `list` and `chart` blocks. Give a `1x1` or `2x1` view with what needs the owner. The master's
  skill `widgets` says what to show on what size.
- **Screens** are lists of blocks from the catalog (`schemas/catalog.json`; in `manifest_format_get` each block
  type has its fields and its rules in `description`, and a refusal names the JSON path and the
  schema rule, such as `missing property 'item'` or `additional properties 'source' not allowed`): `table`, `list`, `card` (the
  record of a record page), `stat`, `chart`, `media`, `actions`, `form`, `custom`, `dialog` (a button that opens
  blocks in a window) and `columns`/`tabs` to lay them out. A screen longer than a screenful is split into `tabs`
  by meaning (the work, its models, its access, its health). A table:
  `{"type": "table", "source": {"collection": "items", "where": "score >= 7", "sort": "-score", "limit": 20},
  "columns": [{"label": "Title", "bind": "title"}], "actions": [{"label": "Rate", "command": "rate", "params":
  {"id": "=_id"}}]}` — `_id` is the id of the record, `record.<field>` the record of a record page, `limit` a page.
  Put the columns in the order that matters: a line shows the first four. Values take `format` (`money` with
  `currency`, `percent`, `date`, `relative`, `duration`, `bytes`, `link`, `tag`), so a bind never formats by hand,
  and `tone` (`ok`, `warn`, `error`, `muted`). Any block takes `when`; a block with a source takes `empty`. A
  history is a `chart`, one point per item in the order of `sort`; `kind` is `line` or `bar`, a missing y is a gap,
  an ISO time x makes a time axis, and only the last 500 points are drawn. Blocks do not group: a total over all
  items is a `stat` (`"value": "sum(rows, .amount)"`), a sum by day is a collection of totals a script writes
  (an `after` trigger); amounts in several currencies are a total per currency. Files are a `media` block.
  A value in the manifest — action `params`, form `values`, a source's `input` — is fixed text or `=<expression>`
  over the item it is on: `=_id` is a number, so that input field is an `integer`. An action passes every required
  parameter of its command; a choice in a row is one action per option. A button whose call the owner may want back
  names `undo`; one whose run makes an external call the row shows names it in `allows` (the press is the owner's
  yes to exactly that call) — not for calls that spend money or change a skill. A row of the module's own collection
  may carry `{"label": "Delete", "command": "deck.record_delete"}`. No button only opens the chat: Deki's button is
  on every screen. A board drag presses the button whose `input` is `status`; a focus list (`"focus": true`) is a
  queue the owner goes through; buttons over a whole list go in its `head`.
  A command the owner fills in by hand is a `form` (`{"type": "form", "command": "score"}`); on a record page
  `{"type": "form", "command": "deck.record_update"}` edits the record. `{"type": "document", "document": "…"}`
  shows a document. Long text is markdown in a `card` field with `"markdown": true`.
  What the catalog cannot show is a `custom` block: an MCP App, one HTML file under `custom/` (`document_save`
  with kind `custom`), `{"type": "custom", "document": "custom/chart.html", "source": …, "actions": […]}`. It runs
  without network and without frames (HTML that names `iframe`, `frame`, `object` or `embed` shows a refusal), in
  an origin of its own. `import { App } from "/ext-apps.js"`; its items, the page's record and the block's `params`
  arrive in `app.ontoolresult` (`structuredContent.items`, `.record`, `.params`); `"fields"` of the source cut what
  each item carries. `app.callServerTool({name: "<project>.<command>", arguments})` calls the commands of its
  `actions` with the button's `params` and the fields `from_block` names. `app.openLink({url: "#/p/<project>/<screen>"})`
  opens a page of deck; `app.readServerResource({uri})` reads `deck://data/<path>` or `deck://file/<id>` (base64
  `blob`, shown by a `blob:` URL; an `src` of the web never loads); `app.requestDisplayMode({mode: "fullscreen"})`
  gives the frame the whole screen before a dialog of its own. Its colors and fonts are the host's:
  `applyDocumentTheme` and `applyHostStyleVariables` from `/ext-apps.js`, CSS only with `var(--…)` of the standard
  MCP Apps names — a fixed color breaks in the dark scheme.
  List screens in `menu`. A record page is a screen with `"record": "<collection>"`; a row's button `{"label",
  "screen": "<that page>"}` opens it. `"screen": "deck/settings"` with `params` `tab` and `dialog` opens a form of
  deck filled. The pipeline of a record is a table over `{"from": "deck.processes_list", "input": {"record": "=_id"}}`.
  A screen declares the `params` it takes (`{"n": "integer"}`) and reads them as `param.<name>`; give an optional
  one a default with `??`. The side panel `side` holds `filter` blocks that set a param of the screen, read by
  every block of it (`"where": "param.coin == nil || coin == param.coin"`).
- **Look and words**: `title` is the name the owner sees on its tile, `icon` a Tabler icon name. Write texts in
  English and add `i18n` with a translation into the owner's language (`{"ru": {"Notes": "Заметки"}}`) for every
  title, label, description, empty text and field name, also enum values wherever they show. `manifest_save`
  answers `untranslated` (add them) and `unused_i18n` (drop them).
- **Changing a manifest**: send `manifest_save` `old` (parts of the text `manifest_get` gives, each found once; a
  run of spaces and line breaks counts as one) and `new` (what replaces each). `document_save` takes `old` and `new`
  the same way; in scripts and custom blocks `old` is exact. Read a large manifest in parts: `fields` such as
  `manifest.commands.<name>` or `manifest.screens.<name>`.
- **Telling the owner**: a script or agent that finds something worth a look calls `owner_notify` with `text`, and
  optionally `screen`, `record` and `actions`; a run with nothing new says nothing. A note about something outside
  the module names it in `cause` (`model:groq/llama-70b`).
- **Starting records** (the first sources) are written after the import with `record_upsert` (a whole record by
  its key), or by the first run of the script that needs them.

## 3. Typical techniques

These live here, not in deck: deck stays small and you combine them.

- **Incoming events** (new mail, postings, files): a script on a schedule polls from a cursor kept in a record,
  writes new items alone, and an agent command runs on what is new (`after` trigger). After sleep the next poll
  takes everything since the cursor — never "the last N hours". The first run takes a window the owner agrees to.
- **Search over big data**: a script builds an index (SQLite, a vector file) in `$DECK_DATA`, the project's own
  data folder that survives runs; a `search` command reads it.
- **Files of `$DECK_DATA` written by more than one run**: a file rewritten whole goes through a temporary name of
  its own run (`f"{path}.{os.getpid()}.tmp"`, then `os.replace`), and a read-modify-write holds a lock
  (`fcntl.flock` on a `.lock` file beside it).
- **Faster than a minute, or always up**: deck sleeps with the owner's Mac. Such work belongs to an external
  service; deck starts it, polls it by a script, and keeps its state in records.
- **External jobs** (generation in a paid service): one command starts the job and stores its id in a record with
  status `waiting`; a script on a schedule checks the waiting records and downloads the files.
- **Documents to PDF** (a CV in Typst): a script with `programs: ["typst"]` writes the `.typ` documents it reads
  with `document_get` into its work folder, runs `typst compile` and puts the PDF into `$DECK_RESULTS`.
- **Process marker**: anything created outside deck carries the id of the deck process that created it, so after
  "outcome unknown" a search finds it instead of creating a duplicate.

## 4. The box

Every program of a module — a script, an MCP server, the CLI of an agent and what its shell starts — runs in a
box of its own that sees and reaches what the rights of the module list:

- files: the system and the runtimes deck finds on `PATH`, its own folder and `$DECK_DATA` to write, the project's
  folders to read. The owner's home, deck's state, secrets and other projects' data are not there. A file of the
  owner elsewhere comes by the owner adding its folder to the project (Access → "Add a folder"): ask for that.
  A program names the path it was refused in its stderr;
- network: through deck's door, to the hosts the module holds `network` on (section 10). The door sees the host,
  not the method: a program may send to such a host. A host is `example.com` (its subdomains too) or `host:port`;
  a name reaches public addresses, so a service on the owner's machine or network is named by its IP:port. The
  shell of an agent reaches its provider and deck, and the hosts of `network` with `"who": "agent"` — not on a
  subscription login (Claude Code, Codex) nor in a module that reads the data of others: there it installs and
  clones nothing itself, a script of the module does. An agent searches with deck's `web_search` and reads pages
  with `web_fetch`;
- inside its box a program binds local ports, starts a headless browser and keeps its caches (`$DECK_DATA/.cache`);
  `files` on `gpu` adds the graphics card;
- no secrets and no keys of the owner in its environment: a secret reaches a module through what its manifest
  names (an HTTP tool, an MCP server).

On a node on a Mac (and deck without Docker there) scripts, MCP servers and ACP agents run in `sandbox-exec`
without local ports or the GPU, and the CLIs of Claude Code and Codex run outside the box.

## 5. Scripts

Write scripts by the module-conventions skill: layout, the contract of stdin and stdout, the one form of a failure
and its types. deck adds the end of stderr to `details`; a failure with no type right after deck answered the
script's last call with an error takes the type of that answer (`input.invalid`, `rights.denied`, `model.limit`,
`model.unavailable`, `network`). A script runs in its box (section 4):

- deck does not tell a script where a project folder is: it reads the path from a record of the module (a
  `settings` collection) you write once the owner added the folder, or from its input;
- its network goes through the proxy deck sets in `HTTP(S)_PROXY`;
- models through `$DECK_MODELS/<connection>/<API path>` with `Authorization: Bearer $DECK_TOKEN` (an OpenAI SDK
  takes it as its base URL with the token as its key): deck puts in the key and keeps the call. The request names
  one model of deck's settings as the API knows it — `model` of a JSON body or form, the path of Google's API, or
  the header `X-Deck-Model` for a `raw` service — anything else is refused. A body with `"model": "@tts"` and the
  connection `@` takes the model the owner assigned. A draft or a trial reaches free models and those of the
  owner's machine; a paid one answers 403: stop and say so, do not retry in a loop;
- deck's API at `$DECK_URL` answers a refusal with its status and a text that says what is missing: print the body
  of an error response, not the status alone;
- no secrets and no keys of the owner; ask for an HTTP tool of the manifest instead, and never ask the owner to
  `export` a key for a module;
- `runtime` is `python`, `node`, `bash` or `exec`; `timeout_minutes` is the ceiling of a run, 10 by default;
- a script runs alone: a helper shared by scripts is repeated in each; the helpers of this skill,
  `helpers/deck_helper.py` and `helpers/deck_helper.mjs`, are the ones module-conventions names;
- deck commands over HTTP: `curl -s -H "Authorization: Bearer $DECK_TOKEN" -d '<json>' "$DECK_URL/commands/deck.record_upsert"`
  (`command_describe` gives the input and `result` schemas). A 200 body is the result itself; an error is plain
  text with a 4xx/5xx status. `record_upsert` takes JSONL and returns `{"written", "ids", "errors"}`;
  `records_list` returns `[{"id", "key", "title", "data": {…}}]`, newest last. A call that waits for the owner
  answers 202 `{"waiting": <ask>}` — deck makes it later, the script goes on. A script a button of the owner
  started gets that yes at the button, so the module keeps no status of its own for "asked" and no button
  "confirm in the cockpit";
- files go into `$DECK_RESULTS`; the result field `files: [{"path": "...", "record": <id>}]` attaches them;
- a script installs its own packages when it runs: the registries are hosts of its `network` (Python: `pypi.org`,
  `files.pythonhosted.org`; Node: `registry.npmjs.org`), the cache lives in `$DECK_DATA/.cache`. Programs it needs
  from the machine go into `scripts[].programs` (`"uv"`, or `{"name": "typst", "install": "brew install typst"}`).
  deck's image carries the programs `python3`, `node`, `uv`, `bash`, `git`, `curl`, `ffmpeg`, `unzip` and
  `chromium`; a script names them all the same, for a deck or a node outside the image. A missing program refuses
  the run and is a red health row with how to get it: ask the owner, never work around it. A script of
  `"runtime": "python"` runs by `uv run --script` on deck's one python (3.11 in deck's image), so its PEP 723 block
  gives it its packages; uv downloads no python, and a `requires-python` above deck's fails as `program.missing`
  that names both versions. Its file stays in the project between runs, so its environment is built once and again
  only when the block changes. Node installs into `$DECK_DATA`:
  ```js
  const dir = process.env.DECK_DATA + "/node";
  require("node:child_process").execFileSync("npm", ["install", "--prefix", dir, "--no-audit", "--no-fund", "left-pad@1.3.0"], { stdio: ["ignore", 2, 2] });
  const leftPad = require("node:module").createRequire(dir + "/")("left-pad");
  ```

### Kinds of models, and APIs a kind does not cover

`model` commands and `deck.model_call` know text, image, tts, stt, music and video in the OpenAI-compatible
protocol; a video is a job deck polls until the clip is ready. A picture, track or clip comes back as a file with
`model` and `cost_usd`; `deck.process_get` names the `models` of a process and their `cost_usd`. A model on the
owner's machine (a local server) is a connection like any other.

Anything else — a service with its own API (fal, Replicate, ElevenLabs, Google Veo's own API) — is a script that
calls it through `$DECK_MODELS`, so the key stays with deck and the call is in its journal. The owner adds the
service as a connection (`raw` for its own paths) and a model with its price per call; the manifest names the
connection under `money`, and the request names the model:

```python
import json, os, sys, urllib.request
inp = json.load(sys.stdin)
req = urllib.request.Request(os.environ["DECK_MODELS"] + "/fal/v1/generate",  # <connection>/<API path>
    data=json.dumps({"prompt": inp["prompt"], "seconds": 30}).encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json",
             "X-Deck-Model": "stable-audio"})  # the model of deck's settings; its API body has no model field
with urllib.request.urlopen(req, timeout=600) as r:
    path = os.path.join(os.environ["DECK_RESULTS"], "track.mp3")
    open(path, "wb").write(r.read())
print(json.dumps({"files": [{"path": "track.mp3", "record": inp.get("record")}]}))
```

A long job of such a service is two commands, never a script that waits for an hour: `start` posts the job and
keeps its id in the record with status `waiting`; `fetch`, on a schedule, asks the status, downloads the file into
`$DECK_RESULTS` when done and sets the record `done`. A file the service gives on its own storage host is
fetched through the proxy, so that host is one of its `network`.

## 6. Agent commands

The prompt says what to do with the input and what the result means; facts and criteria live in md documents it
reads, and a skill carries a reusable way of working. The agent reaches deck through the deck MCP server and the
commands of projects, not deck's HTTP API. Where a tool is missing (a change of many records at once), give the
module a script or command for it. A provider with a shell (Claude Code, Codex, an ACP agent) works in its own
folder and the project's data folder, runs shell commands in its box without asking; a call that needs a right
the owner has not answered waits for them. An agent that works in a folder of the owner (a code repo) gets
`"folder": true` in `executor` (or the folder's name, declared under `files`); a second agent there waits. List
the skills it needs in `executor.skills`; a skill may not grant tools — rights come from deck.

The owner picks the models of each agent command (`assignment_set`); the manifest says what the command needs and
suggests one (`provider`; `"free"` puts the free models first, for routine work). `requires` lists `no_cloud` for
personal data that must stay on the owner's hardware and `images` for a model that reads pictures. When the
result is wrong without research (news, changelogs), the prompt names `web_search` and `web_fetch`, and `calls`
lists them; leave the web out when a script brings the pages. Repeatable mechanics belong in a script.

Write the prompt so it works on a small model too: say which deck tool to call for what, what each call of
`calls` answers and its exact shape (a record's `data` is an object, not a JSON text), one to three example inputs
where the schema does not say all, and that the result is handed in at the end (`result_submit` when the provider
has no output by schema). The tool of a command shows the agent its `result` schema and an input of the `examples`
of its parameters, so a command an agent calls has both. An agent command that changes records, documents or
files gives in its result `changes`, one short line each (the module-conventions skill). A small model copies
examples word for word and misses checks plain in the text: give it the shape of the result without sample
values, let the script do every check a pattern can do, and let the script decide what reaches the owner. An
agent that judges many items hands in all verdicts in its result and an `after` script writes them. Try the
command on the model it will run on before the hand-over.

## 7. Confirmations

Each "yes" the owner gives by habit is a failure of the module. Count what will wait in a normal week: a tool
that reads names `"right": "data"` or `"network"`; a draft or unpublished post is still `send`, and the hand-over
asks for always.

An agent turn that read text from outside deck — a page, an answer of a tool or MCP server, the browser, data of a
module that holds such text — and holds something of value acts outside by the owner's yes to each call: `send`,
`money`, `modules`, `network` on `*`, a script or tool of a module holding a way out wait for a once, whatever
always says. So keep reading apart from acting: a script fetches pages, or a reader (an agent whose `calls` are
`deck.web_search` and `deck.web_fetch` alone) reads them; the agent that acts reads its records.

## 8. Build, import, try

1. Write the module as a folder: `manifest.json`, `prompt.md` (its first line is the one-line description deck
   and the master choose the project by), `prompts/*.md`, `scripts/*`, `skills/<name>/SKILL.md`, other md
   documents. `prompt.md` has kind `prompt`; the other md come in as `doc`. Keep test data outside the folder.
2. `project_import` with the absolute folder path creates a **draft** (the folder is in your data or in a folder
   the owner gave a project; any other the owner imports). A refused import leaves nothing behind; fix and import
   again. Then change the draft with `manifest_save` and `document_save`, sending only what changes. The answer of
   `manifest_save` lists in `screens` each block that fails on the records there are now: fix them.
   `manifest_check` runs the same checks and saves nothing (`ungranted`, `screens`, `untranslated`).
   `screen_capture` draws a screen as the owner sees it (`#/p/<module>/<screen>`). `project_readiness_get` is what
   the module needs to work: rights and whether granted, secrets, missing programs, roles without a model. Tell
   the owner what is not `done`.
3. `command_try` runs a command as a trial: a call that needs a right is declined, a call of an HTTP or MCP tool
   waits for the owner's yes; the `after` commands are tried too, unless `only`. Until the owner enables a draft it
   holds none of the rights it declares; ask the owner to grant one a trial needs on the draft's card. To try a
   change of an enabled module, import a draft copy under another name, try there, then save the change and tell
   the owner the copy can be deleted: a trial in the enabled module takes `live` and is refused while a real run of
   the command goes on. A trial runs as you, the master: leave `callers` out unless a command must be closed to
   someone, and keep `master` in a list you give. A trial is not a sandbox: records and documents it writes are real and stay,
   and so is what scripts write to `$DECK_DATA`. `process_get` of a trial lists in `left` what it and its chain
   left; before the hand-over delete what the owner should not start with (`record_delete`) and say what you kept.
   `process_log_get` shows the events of a process (`only` `errors` for what failed); wait with `process_get` and
   `wait`. Time the trial on the provider the command will run on and set `timeout_minutes`, batches and the
   schedule by it. A trial that ends `done` is not yet a proof: check what the owner will see (records,
   documents), and `health_list` after a save that adds programs or MCP servers.
4. A draft does not run on its schedule. The owner enables it.

## 9. Moving modules and skills

- **Export a module** into a new or empty folder: `documents_list` of the project, then `document_get` of each
  document of kind manifest, prompt, script, skill, custom and doc that has no `owner_kind`, written under its
  name. Artifacts are data, not the module. `project_import` of that folder gives the module back as a draft; kinds
  come from the path, so keep scripts listed in `scripts[].file` and custom blocks under `custom/`.
- **Import the owner's Claude Code skills**: for each folder `~/.claude/skills/<name>/`, save `SKILL.md` first with
  `document_save`, kind `skill`, name `skills/<name>/SKILL.md`, no project (a skill of the workspace); if deck took
  it, save the other text files as `skills/<name>/<path>`. Skip a file whose text equals what `document_get`
  returns, hidden files, binaries and files over 1 MB. deck refuses skills that grant tools (`allowed-tools`,
  `hooks`) and path parts that do not start with a letter or digit — tell the owner what did not come in and why.

## 10. Rights

A module declares in `rights` what it may ask the owner for, with why in a short sentence of its own; what it does
not declare it cannot ask for, and a declaration grants nothing. Its own records, documents and files need no
right. deck's catalog:

| `right` | `what` |
|---|---|
| `network` | hosts, `*` all public ones; with `"who": "agent"` the shells of its agents |
| `login` | `claude-code`, `codex`: its agents work on the owner's subscription |
| `data` | `other`, `other.items`, `other/a.md`, `other:models`, `*:runs`; its own tool or `server.tool` that reads with a key |
| `files` | `gpu`, a folder of the project by name, `*` |
| `money` | a connection, a model, an `@role`; `run_usd`, the most a run spends by the owner's always, in US dollars |
| `send` | its command or tool, `server.tool`, a command of deck, a site the browser acts on |
| `modules` | another module, `module.command` |

Ask for roles (`@text`, `@image`, `@tts`, `@stt`), not the owner's connections or model names. Give `money` a
`run_usd` the work needs: past it an agent's call waits for the owner's once and a script's call is refused.
Enabling grants what takes no passkey: `network` of programs on named hosts, `files`, `data` of its own services
and `:models`/`:runs`. `network` on `*` and the data of other modules the owner grants in Access → Rights; `login`,
`money`, `send`, `modules` and the network of agents the owner answers when first used: once, for this work, always
or no (a no is kept). A turn that needs a login or its agents' network waits for that answer before it starts. A
save that declares something new lists it in `ungranted`: tell the owner what to grant and why. An edit never
widens what is granted, and always holds to the tool or command as it was: a changed one asks again.

## 11. Hand over

Tell the owner in a few plain sentences: what the module does and when, what the trial showed, and the rights it
declares and why. Do not enable it yourself.

## 12. Improve this skill

When the owner fixes by hand a module you built, or this skill was silent or wrong, propose a fix of the section
that led you there: `document_propose`, name `skills/module-builder/SKILL.md`, no project, the whole text with its
`if_revision` from `document_get` and `reason`. Change the rule itself and keep the skill general; no dated notes,
examples of one module or history. The skill keeps to 8000 words: what you add, take the same out. Say in the
hand-over what you proposed.
