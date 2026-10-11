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
`command_try`, `process_get`, `process_log`, `records_list`, `records_upsert`, `files_list`,
`projects_list`, `triggers_list`, `models_list`, `health_list`, `asks_list`, `command_describe`. Ask a read for only
the fields you need (`fields`; of `records_list` they are the fields of the data of each record). Some commands only the owner runs:
`secret_set`, `oauth_start`, `browser_login`, `assignment_set`, `model_set`, `project_enable`; say which one and
why when the module needs it. The owner does them in two places. The Settings of the project (Settings →
Projects → "Settings" on its row): "Enable" with the rights enabling grants, pause, the models of
each command, folders, the secrets its tools take. deck's Settings tabs: Projects (create, import, a trial run),
Models (assignments, a model and its agent), Connections (where models are reached, and the other secrets: a
name and a hidden value), Tokens, Health; sign-ins wait in the cockpit. So name
the place and the value ("Settings → Connections → Secrets: `mail-key`, the API key of …"), never a `curl`; the fields of
these forms are the input of their command, which `command_describe` gives; `commands_list` marks these commands
`owner_only` with `form`, the address of the form to link to, and `form_params` that fill it.

When the module needs something only the owner gives — an API key or token, an account or sign-in, a right of
the manifest, a program installed on the machine, a paid call in a trial — stop and ask the owner in the chat:
what, why, and where to put it. Do not work around it: no other service or free key in its place without the
owner's word, no `internal` class or wider right just to get past a refusal, no key written into a document,
a script or the environment, no copy by hand of what deck or its sandbox refuses. Build the rest, then go on
with that part once the owner has done it.

## 1. Interview

A draft sends nothing out until the owner enables it, so build first and ask only what a sensible default
cannot settle: what leaves deck, what costs money, and choices that change the shape of the module. Everything
else takes a sensible default, and the hand-over lists every choice you made yourself. Ask one question at a
time with your question tool; offer 2–4 options, the first one your recommendation. What to settle:

- the outcome: what the owner wants to see each day or week, and what "done" means for one item;
- the inputs: where items come from, how often they change, whether a login is needed;
- judgement vs mechanics: which steps need taste (an agent) and which are repeatable (a script);
- stages: whether items move through stages the owner acts on (a `status` field of the record), and which;
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
  what belongs to them, as below) that many days after they were created. What belongs to a record goes with it: `"folder": "'videos/' +
  slug"` is its folder in the data folder, and a collection whose records are of a record of another names it,
  `"parent": {"collection": "videos", "key": "video"}` (the expression of the parent's key). Deleting the record
  takes those records, their files, the files of the processes whose input field `record` is its id, and their
  folders, and stops those processes still open, all listed to the owner first, for good. A folder another record
  also gives, or one inside or around another's, stays; an empty segment, `.` or `..` in the path is no folder
  (so a record without its `slug` takes none), and a path deck cannot work out stops the deletion. So give each
  record a folder of its own by its key, and lay out the data folder by record:
  `<collection>/<key>/` for what a record has, `cache/` for what can be made again, scripts as documents of the
  module, nothing loose at its root. `key`, `display`, `where` and `bind` are expr-lang
  expressions: a field named like one of its functions (`date`, `now`, `count`, `len`, `sum`, `max`, …) reads as
  the function there, so name such fields `day`, `ops` and so on.
  `records_list` returns every record of a collection (the newest `limit` when given; an agent passes one), and
  `record_update` replaces the whole record: work
  over hundreds of records (filtering, batching, merging new fields into old ones) belongs in a script, and an
  agent gets only the slice it judges, from the script's result or its input.
- **Commands** are the verbs. Each has a class: `internal` (records, documents), `reversible`,
  `external` (leaves deck). Choose the executor:
  - `script` for mechanics that repeat the same way (fetch, parse, poll, convert, index) — cheap and exact;
  - `agent` for judgement (rate, summarise, write) — give it a prompt file, skills and a result schema; an
    agent that reads what strangers wrote (mail, pages, feeds) gets `"calls"`, the list of commands it may
    call (`["deck.records_list", "<project>.score"]`, or every command of a project as `"<project>.*"`), so a
    page cannot steer it into the rest;
  - `tool` for exactly one call of a tool: `{"type": "tool", "tool": "<http tool>"}`; the tool gets the
    command input as it is (declare the same input), and the process result is what the tool answered;
  - `model` for one call of a model without an agent: `{"type": "model", "kind": "text", "prompt_file": …}` —
    `text` answers the prompt with the input by the result schema, `image` draws the prompt with the input, `tts`
    speaks the input field `text` (the prompt says how), `stt` writes down the one input field of format file,
    `music` makes a track of the prompt, `video` a clip of it from the input file fields `first_frame` and
    `last_frame` when given (both optional; a clip takes minutes, and the command waits for it as an agent works);
    a picture, a track, speech or a clip is a file of the result. The model is the one the owner assigned to the kind (with
    fallbacks); `"model"` names another, `"options"` adds fields to the request (`voice`, `image_config`).
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
  An input field of `"format": "json"` is text with JSON for the caller; `"{field}"` alone in a JSON body puts
  the value it holds (a list, an object), so one tool serves every shape of body.
- **Models** are deck's, declared once in its settings (connections such as OpenRouter or Ollama, their models with
  prices, and which models do text, image, tts, stt, music, video, with fallbacks); the module holds only its business
  logic and calls them through deck, never with a key: a command of type `model`, `deck.model_call` from a script or
  agent, or an API path through `$DECK_MODELS` (below). Every call names a model of deck's settings or an
  `@<kind>`; another model is refused. A tool or MCP server of the manifest never gets the key of a connection
  (the save refuses it). The manifest names what it calls in `models`: a connection, a model by name or
  `@text`/`@image`/`@tts`/`@stt`/`@music`/`@video` for the assigned ones; deck keeps each call with its cost in its journal,
  and the settings of the module list the models it called. A model the module needs and deck lacks: propose it
  with `deck.model_set` (connection, model, kind; the price comes from the catalog of the connection) — the call
  waits for the owner's button. deck has no limits of money: the owner sets them at the provider, so a module never
  loops on paid calls.
- **MCP servers** (`tools.mcp.servers`, the usual `mcp.servers` shape): `command`/`args`/`env` (values may use
  `$secret.<name>`) or `url`; a `command` server reaches the web only through deck's proxy, to the hosts of
  `domains.read` and `domains.act`, never the owner's machine or network unless a domain names it; `toolFilter` lists the tools the module uses, `classes` their classes — every
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
  `files_list` of a record); scripts and agents get the path of a read-only copy. For a trial, upload
  a sample with `file_upload` (the file in base64) and pass the id it returns. A result may give such
  a file to a record: `files: [{"file": <id>, "record": <id>}]`.
- **Documents to PDF** (a CV in Typst, say): a script with `programs: ["typst"]` writes the `.typ` documents it
  reads with `document_get` into its work folder, runs `typst compile` and puts the PDF into `$DECK_RESULTS`.
  To compare two revisions of a document, read both (`document_get` with `revision`) and say what changed;
  screens have no diff block.
- **Reads**: `reads: [{"project": "other", "collections": [...], "documents": [...]}]` lets agents and scripts
  read those (`records_list`, `document_get` with `project`), and a screen shows them with `"source": {"from":
  "deck.records_list", "input": {"project": "other", "collection": "items"}}`, its fields under `data.`.
  Nothing ever writes to another project.
- **Results** are JSON by schema. Decisions use typed shapes: a choice with probabilities and confidence,
  a score 2–10 with confidence, yes/no with a probability. A result of `null` means "nothing to do", and an agent
  may give it only where the result schema takes it: `"type": ["object", "null"]`; with `"type": "object"` a null
  is not by the schema.
- **Triggers**: `cron` for schedules, `after` when another command of the project finishes with a result that
  is not `null` (a poll with nothing new returns `null` and the chain stops). The command a trigger starts gets
  the trigger's fixed `params` plus `{"process": <id>}` of the finished one for `after`, so its input declares
  that field as `integer`; a later step that needs an earlier result
  reads it with `process_get`, so keep what the chain needs in records rather than passing it along. Cycles are refused. A chain runs at most five steps deep, counted from the call that started it (a button, a schedule or a trial): a sixth `after` step does not start, and a note of the feed says so. Keep chains short — two `after` triggers on the same step run side by side, so a step that only prepares something can run beside the next one instead of before it. After the Mac slept, each missed schedule runs once; `"skip_missed":
  true` drops the missed run instead (a morning digest that is stale by noon).
- **Stages** the owner acts on are a `status` field of the record (an `enum`), shown as a board (`"board":
  "status"` on a `list`), where the owner drags an item to its next stage; the other fields of a record the owner
  edits on its page (a `form` of `deck.record_update`), and a command takes the records of its status with
  `records_list` and `where`.
- **Work in progress** on a record is a value of its `status` (`applying`) the command sets first and its script
  clears at the end: the row shows it by `tone` or a `tag` cell, and the button hides by `"when": "status !=
  'applying'"`. A button whose second press must not start the work twice passes `"retry_key": "='apply:' +
  string(_id)"` in `params`: a call with the key of a running process of that command returns that process.
- **A last lane that keeps growing** (`done` of a board) is cut by the source of the board, `"where": "status !=
  'done' || date(_updated) > now() - duration('168h')"`, and the whole history is a separate table of that
  status; `limit` cuts every lane at once.
- **Expressions are JSON text**: a line break is the escape `\n` inside the quotes of the expression, written
  `\\n` in the manifest and in its `old` and `new` parts (`"text": "=title + '\\n' + note"`); a JSON `\n` alone puts a raw
  line break into the quotes, and the expression is refused. `text[:80]` and `len(text)` count characters.
- **`folder` may be one file** a record has (`"folder": "'tracks/' + slug + '.mp3'"`): deleting the record
  deletes that file.
- **Derived state** beside the records (an index, a `tasks.json`, totals) hears nothing when the owner deletes a
  record with `deck.record_delete`: rebuild it from the records each time it is made, so a deleted record drops
  out at the next run, or delete through a command of the module (a status `deleted` its scripts honour) and
  give its screens that button instead.
- **Documents** agents and scripts write are artifacts only: each belongs to one record. A daily digest or report is an artifact of a record made for it (one record a day); `prompt.md`,
  `prompts/*.md` and other module documents are written by you, not by the module at run time.
- **Screens** are lists of blocks from the catalog (`schemas/catalog.json`; in `manifest_format` each block type
  has its exact fields and its rules in words in `description`, and a refusal names the JSON path and the
  schema rule, such as `missing property 'item'` or `additional properties 'source' not allowed`):
  `table`, `list`, `card` (the record of a record page), `stat`, `chart`, `media`, `actions`, `form`, `custom`,
  `dialog` (a button that opens blocks in a window) and `columns`/`tabs` to lay them out. A screen that grows
  long — more than a screenful of blocks the owner scrolls through — is split into `tabs` by meaning (what the owner
  looks at together: the work, its models, its access, its health), never one long sheet; dialogs stay on the screen,
  their buttons beside its title. A table:
  `{"type": "table", "title": "Best", "source": {"collection": "items", "where": "score >= 7", "sort": "-score",
  "limit": 20}, "columns": [{"label": "Title", "bind": "title"}], "filter": ["status"], "actions": [{"label": "Rate", "command":
  "rate", "params": {"id": "=_id"}}]}` — `where` and `bind` are expressions over record fields (`_id` is the id
  of the record, `record.<field>` the record of a record page),
  `sort` is a field with `-` for descending, `filter` lists fields the owner narrows by. A column, a `card`
  field and a `stat` take `format`, and the client shows the raw value in the owner's locale and time zone:
  `number`, `percent` (0.42 is 42 %), `money` with `currency` (`{"label": "Price", "bind": "price", "format":
  "money", "currency": "USD"}`), `date`, `datetime`, `relative` (5 min ago), `duration` (seconds), `bytes`,
  `link`, `tag` — so a bind is never `string(round(change * 100)) + '%'`. A column, a card field, a `list` line
  (`item.tone`) and a `stat` (over `rows`) take `tone`, an expression giving `ok`, `warn`, `error` or `muted`:
  `"tone": "change < 0 ? 'error' : 'ok'"`; any other value is no tone. A block with a source takes `empty`, plain
  text it shows when there are no items (`"empty": "No items yet"`); a `stat` with `empty` shows it instead
  of its value, so the last price is `{"type": "stat", "source": {"collection": "prices", "sort": "-at",
  "limit": 1}, "value": "rows[0].price", "format": "money", "currency": "USD", "empty": "No prices yet"}` with no
  `len(rows) > 0 ? … : '—'`. Any block takes `when`, an expression over the fields of the page's record and
  `rows` of its own source; where it does not hold the block is not on the screen (`"when": "len(rows) > 0"`
  for a list of misses, `"when": "status == 'open'"` on a record page). A row of a `table` or `list` whose
  `"primary": true` button opens a screen opens it on a click anywhere in the row. A history is a `chart`, one point
  per item in the order of `sort`: `{"type": "chart", "title": "Price", "kind": "line", "source": {"collection":
  "prices", "sort": "at"}, "x": "at", "series": [{"label": "Value", "y": "price"}, {"label": "Expected", "y":
  "forecast"}], "format": "money", "currency": "USD", "empty": "No prices yet"}` — `kind` is `line`, `bar` or
  `area`, a missing y is a gap, an ISO time x makes a time axis, and only the last 500 points by the sort are
  drawn — `limit` keeps the first items, so do not cut a history with it. Blocks draw items as they are and do
  not group them: a total over all items is a `stat` (`"value": "sum(rows, .amount)"`), and a sum by day or by
  category is a collection of totals a script writes after the records change (an `after` trigger), which a chart
  or table then shows. `currency` is one code for a column or block: amounts in several currencies are a total per
  currency (a field `currency` in that collection, a row each), or a `custom` block; never one sum of them all.
  Files are a `media` block: `{"type":
  "media", "source": {"collection": "scans"}, "item": {"file": "file", "title": "name"}}`, where `file` is the
  id of a stored file of the project (a result file of its run, an attachment of its record; a result names the
  files it kept by that id, in `files[].file` and in place of a `file` field), its digits, or a path in its data
  folder (`"charts/today.png"`); a name is not a file; a picture is a thumbnail, a PDF shows in place, a video or a sound plays in
  the browser's own player, anything else is a name with a download. A player with marks is a `custom` block
  (MCP Apps) reading the file via `resources/read` of `deck://data/<path>`; its buttons send the second and a note
  to a command of the module.
  A value in the manifest — action `params`, `screen`, `record`, form `values`, a source's `input` — is fixed
  text or `=<expression>` over the item it is on (a form and an input: the record of the page) and `param`:
  `=title` passes the field's value with its type (`=_id` is a number, so that input field is an `integer`), and
  a fixed value becomes the type of its input field (`"days": "7"` to an `integer` is 7). An action passes
  every required parameter of its command; deck asks for nothing that is missing, so a choice the owner
  makes in a row is one action per option. A button whose call the owner may want back names the call that takes
  it back, `"undo": {"command": "unrate", "params": {"id": "=_id"}}`, offered for a moment after it went
  through. A row of the module's own collection or its record page may carry the one deck command a button calls,
  `{"label": "Delete", "command": "deck.record_delete"}`, with no other params and no undo: the owner deletes that
  record with what belongs to it, always seeing the list first (not on a `custom` block). `{"label": "New task", "chat": true}` alone opens the master's chat beside the screen in a new
  topic, ready for the owner's voice; a link with `?chat=new` does the same. A list of stages is a board:
  `"board": "status"` on a `list` of the module's collection lays its items in a lane for each value of that enum
  field, and dragging a card to a lane presses its button whose `input` is `status` — a command of the module that
  takes the record id and `status` (a small script setting the field), `{"label": "Move", "command": "move",
  "params": {"id": "=_id"}, "input": "status"}`, its `options` the lanes when not every value is one (on a phone that
  button picks the lane), so the board needs no custom block. The same button in a `table` is a picker in its
  cell: `"pick": "Move"` on the column of `status` shows the value as a dropdown of the button's `options`, and a
  pick presses it. A command the owner fills in by hand is a `form`:
  `{"type": "form", "command": "score"}` asks for the command's input by its schema and shows the result by the
  result schema; a command that keeps its result writes the records itself; a field picked from a source takes
  `"choices": {"note": {"source": {"collection": "notes"}, "value": "_id", "label": "title"}}`, and a field with
  `"format": "project"` offers the projects by itself; on a record page
  `{"type": "form", "command": "deck.record_update"}` edits the record, `immutable` fields read only. An id the owner
  need not see goes in `fixed`: `"values": {"slug": "=slug"}, "fixed": ["slug"]` sends it unshown and unchanged.
  `{"type": "document", "document": "prompts/criteria.md"}` shows a
  document of the project, on a record page one of the record (an artifact): markdown drawn, an md document
  edited by the owner as a new revision, its revisions and going back to one; every project also gets a
  Documents screen from deck. Long text is markdown in a `card` field with `"markdown": true`; everywhere else
  text stays plain. What the catalog cannot show is a `custom` block: an MCP App — one HTML
  file under `custom/` (`document_save` with kind `custom`), `{"type": "custom", "document": "custom/chart.html",
  "source": …, "actions": […]}`. It runs without network, in a frame of an origin of its own (no storage, no
  cookies, no reach into deck's page) and without frames: HTML that names `iframe`, `frame`, `object` or `embed`
  anywhere, even in a string, shows only a refusal, and making one at run time throws. `import { App } from "/ext-apps.js"`, the items of its
  source, the page's record and the block's `params` arrive in `app.ontoolresult` (`structuredContent.items`,
  `.record`, `.params`): `"params": {"mode": "voice", "title": "=title"}` takes fixed text or `=<expression>` like
  `values` of a form, so one HTML serves two screens with their own settings instead of two near copies; and
  `app.callServerTool({name: "<project>.<command>", arguments})` calls only the commands of its `actions` (the
  button whose fixed `params` the arguments do not contradict; any but an internal command waits for the owner's yes,
  one at a time). A window opens on the host, not inside the frame: `app.openLink({url: "#/p/<project>/<screen>?dialog=<name>"})`
  opens a dialog or page of deck (a page outside deck is refused), and `app.requestDisplayMode({mode: "fullscreen"})`
  gives the frame the whole screen before its own `<dialog>`.
  A picture, video or audio of the module comes by `app.readServerResource({uri})`: `deck://data/<path>` a file of
  its data folder (each part URL-escaped), `deck://file/<id>` a stored file of the module; `contents[0]` holds the
  file whole in base64 `blob` with its `mimeType`, up to 200 MB. Show it by a `blob:` URL
  (`URL.createObjectURL(new Blob([Uint8Array.from(atob(c.blob), (x) => x.charCodeAt(0))], {type: c.mimeType}))`);
  an `src` of `/api/files/…` or of the web never loads, and a file of another module is refused.
  Its colors, fonts and corners are the host's: `app.getHostContext()` and `app.onhostcontextchanged` give
  `theme` (`light`/`dark`) and `styles.variables` by MCP Apps names (`--color-background-primary`,
  `--color-text-primary`, `--color-text-secondary`, `--color-border-primary`, `--color-ring-primary` for the
  accent, `--font-sans`, `--font-text-sm-size`, `--border-radius-md`); apply them with `applyDocumentTheme` and
  `applyHostStyleVariables` from `/ext-apps.js`, again on each change, and write CSS only with `var(--…)` of
  them — a fixed color breaks in the owner's dark scheme. List screens in `menu`. A record page is a screen with
  `"record": "<collection>"`; a row's button `{"label", "screen": "<that page>"}` opens it with the row's record;
  it takes no `record` or `project`. How the pipeline of a record goes is a table on its page over
  `{"from": "deck.processes_list", "input": {"record": "=_id"}}` — the processes whose input field `record` is its
  id (columns `command`, `state`, `question` when one waits for the owner; `after` triggers start the next).
- **Look of a module**: `title` is the name the owner sees in the menu (the project name is for code), `icon` of
  the project, a screen, a block or a button is a Tabler icon name; blocks flow in a grid, `span` (1, 2 or "full")
  widens one, `description` says under a title what a block is for. Write the texts in English and add `i18n`
  with a translation into the owner's language (`{"ru": {"Notes": "Заметки"}}`) for every title, label,
  description and empty text, also when you change them later. A button to a screen takes `params`; the screen
  declares them with their types (`"params": {"title": "string", "n": "integer"}`) and reads them in expressions
  as `param.<name>`: `"values": {"title": "=param.title"}` fills a form, `"input": {"days": "=param.n ?? 7"}` a
  source, `"when": "param.n > 1"` a block. An input that gives nothing or fails is an error on its block, not a read
  without it, so give an optional param a default with `??`.
- **Changing a manifest**: send `manifest_save` `old` (parts of the text `manifest_get` gives, each found exactly
  once; take enough around a part to make it unique) and `new` (what replaces each).
- **Telling the owner**: a script or agent that finds something worth a look calls `notify` with `text`, and
  optionally the `screen` (and `record`) it opens and `actions` (`[{label, command, params}]`); it becomes a line
  in the cockpit feed. The caller decides it is worth it; a run with nothing new says nothing.
- **Remembering what was seen**: `records_upsert` replaces a whole record by its key, so write every
  required field. `record_get` reads one record by its id ; `records_list` gives the newest. `retention_days` deletes records with their documents: a collection that remembers what the
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
- of the owner's home folder it reads only the runtimes deck finds on `PATH` (an nvm node, a pyenv python) and
  the project folders; the system folders read as usual. A file of the owner elsewhere is opened by the owner
  adding its folder to the project (its settings, Access): ask for that, never copy the file around the sandbox.
  deck does not tell a script where a project folder is (no variable, and `deck.projects_list` and
  `folders_list` are closed to scripts), so a script that walks the folder reads its path from a record of the
  module (a `settings` collection) that you write once the owner has added the folder; a command input works too.
  A program names the path it was refused in its stderr (`Operation not permitted`), and the error of a failed
  script ends with where the owner opens a folder; MCP servers and ACP agents of the project read the same;
- network only to the manifest's `domains.read`, through the proxy deck sets in `HTTP(S)_PROXY` (Node's `fetch`
  takes it too; `$DECK_URL` answers through the proxy or without it); a domain is a
  host (`hf.co` takes its subdomains too) or `host:port`, never a URL or a public suffix such as `co.uk`; a name
  reaches only public addresses, so a service on the owner's machine or network is named by its IP or IP:port;
- models through `$DECK_MODELS/<connection>/<API path>` with `Authorization: Bearer $DECK_TOKEN` (an OpenAI SDK
  takes it as its base URL with the token as its key): deck puts in the key and keeps the call; `deck.models_list`
  gives the connections and models of deck's settings and what is assigned to the workspace and this project. The request names
  one model of deck's settings as the API knows it — `model` of a JSON body or a form, the path of Google's API,
  or, for a `raw` service whose API has no such field, the header `X-Deck-Model` — and anything else is refused:
  another model, OpenRouter's `models` or `route`, and on an `openai`, `google` or `anthropic` connection any path
  that is not a call of a model (files, batches, the account); a poll `GET videos/<id>` of a job names no model,
  but on a `raw` connection every request, the poll too, sends `X-Deck-Model`. A
  body with `"model": "@tts"` and the connection `@` takes the model the owner assigned, a fallback when it
  refuses. A draft or a trial reaches only free models and those of the owner's machine: a paid one answers 403
  and is not made; stop and say so, do not retry in a loop;
- no secrets and no keys of the owner; ask for an HTTP tool of the manifest instead. Never ask the owner to
  `export` a key for a module: on macOS a confined program reads the environment of the owner's processes, so
  keys live only in deck secrets;
- `runtime` is `python`, `node`, `bash` or `exec` (a program that runs by itself); `timeout_minutes` is the
  ceiling of a run, 10 by default, up to 240;
- no local port and no GPU: a script cannot serve a page to a browser (a Remotion render) or run a model on Metal
  (MLX). That work is an agent command on Claude Code in a project with network: its shell binds ports, uses the
  GPU and keeps its caches like the owner's own; the script readies the inputs and the agent renders;
- a script runs alone: its folder holds only its own file, so a helper shared by scripts is repeated in each
  (or read with `document_get` into the work folder);
- deck commands over HTTP: `curl -s -H "Authorization: Bearer $DECK_TOKEN" -d '<json>' "$DECK_URL/commands/deck.records_upsert"`
  (`command_describe` gives the input and `result` schemas while you write the script; works through the proxy or without it).
  A 200 body is the command's result itself, with no wrapper; an error is plain text with a 4xx/5xx status, 410
  when the process has finished; `records_upsert` takes JSONL
  and returns `{"written", "ids", "errors"}`; `records_list` returns `[{"id", "key", "title", "data": {…}}]`,
  newest last, `process_get` `{"id", "command", "state", "result", "error", …}`, `documents_list`
  `[{"id", "name", "kind", "owner_kind", "owner_id"}]` (an artifact of a record has `owner_kind` `record`); a script may call commands and tools of its own project that
  allow `agent` callers; a call that waits for the owner answers HTTP 202 `{"waiting": <ask>}` — deck makes
  it later, the script goes on;
- files go into `$DECK_RESULTS`; the result field `files: [{"path": "...", "record": <id>}]` attaches them;
- a script installs its own packages when it runs; there is no `packages` field. The registries go into
  `domains.read` (Python: `pypi.org`, `files.pythonhosted.org`; Node: `registry.npmjs.org`), the package
  manager goes through the proxy, its cache lives in `$DECK_DATA/.cache` (uv, npm, Hugging Face) and stays between
  runs, the install counts in `timeout_minutes`; a project without the registries in `domains.read` has no packages.
  Programs it needs from the machine go into `scripts[].programs`: a name (`"uv"`) or a name with how the owner
  installs it (`{"name": "ffmpeg", "install": "brew install ffmpeg"}`). A missing program refuses the run at once and
  is a red health row of the command with that text; ask the owner to install it, never work around it — the
  check button of the row, or the next run, finds it. Python: `"runtime": "exec"` and a PEP 723 block:
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

### Kinds of models, and APIs a kind does not cover

`model` commands and `deck.model_call` know text, image, tts, stt, music and video, asked in the OpenAI-compatible
protocol: a picture of `images/generations`, music of `audio/music` (`input`, `seconds`, like speech), each of chat
completions with image or audio out when the API has no such path; a video is a job of `videos` (OpenRouter's
shape: `first_frame`/`last_frame` go as `frame_images`, `options` such as `duration`, `resolution`, `aspect_ratio`,
`generate_audio`) that deck polls until the clip is ready, at most 30 minutes, and a job a service took goes to no
other model; an agent that calls it hears progress meanwhile. A picture, a track or a clip comes back as a file with
`model` (the model of deck's settings that made it, a fallback too) and `cost_usd`, for the module's own accounting
and credits. Other results are as the API gave them; `deck.process_get` of the process names its `models` and their
`cost_usd` (none while a model has no price), speech priced per character and recognition per second too. The models of a kind and their fallbacks are the owner's assignment; one that does not answer turns red in health while
the next takes the work. A model on the owner's machine (a local server, as whisper.cpp) is a connection like any
other. A connection of another API, such as Google's own, is reached only from a script.

Anything else — editing a picture by a sample (`images/edits`, a form), a service with its own API (fal,
Replicate, ElevenLabs, Google Veo's own API) — is a script that calls the API through `$DECK_MODELS`, so the key stays with
deck and the call is in its journal. The owner adds the service as a connection (Settings → Connections: `raw` for a
service with its own paths, such as Veo's long jobs, fal or Replicate; `google` only for `models/<m>:generateContent`)
and a model with its price per call; the manifest
names the connection in `models`, and the request names the model. One call, the answer a file:

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

A long job of a service with its own API (minutes) is two commands, never a script that waits for an hour: `start` posts the job
through `$DECK_MODELS`, keeps the job id in the record with status `waiting` and ends; `fetch`, on a schedule
(`every 5m`), reads the waiting records, asks the status path of the API through `$DECK_MODELS`, and when the
job is done downloads the file into `$DECK_RESULTS` and sets the record `done`. A file the service gives as a URL
on its own storage host is fetched through the proxy, so that host goes into `domains.read`. Ask the owner for the
price per call of such a model: a service that reports no `usage.cost` is counted by it, else without a cost.

A tool of the manifest called from a script (`POST $DECK_URL/commands/<project>.<tool>`) that answers with a
file returns `{"file": "<name>", "type", "size"}`: the file is `$DECK_RESULTS/<name>`, and `results/<name>`
hands it to another tool or a `file` field.

## 5. Agent commands

The prompt says what to do with the input and what the result means; facts and criteria live in md
documents it reads, and a skill carries a reusable way of working. The agent reaches deck only through the
deck MCP server. A provider with a shell (Claude Code, Codex, an ACP agent) runs it in its auto mode: the agent works in its own
folder and in the project's data folder (the prompt names it; indexes, caches and state live there between
runs), runs shell commands, and searches and fetches the web without asking; the provider itself refuses
what looks risky and asks the owner only about that. The shell of Claude Code renders, binds local ports, uses the
GPU and keeps its caches like the owner's own shell; the shells of Claude Code and Codex reach the whole network,
whatever `domains.read` names, and the project's settings say so to the owner; the shells of ACP agents reach only
package registries (PyPI, npm, the go proxy), GitHub, Hugging Face and the project's `domains.read` (`"*"` opens the
whole network, a right the owner gives); a project with `no_network` closes every shell. Secrets and deck's own state stay closed to it; on
Claude Code with network its shell is the owner's, so another project's data is not a wall. A deck tool of class `external` outside `allowed_external` still waits for the owner. Set
`"no_network": true` for a project with personal data (finance, health, mail): its agents get no web and no
network of their own and reach outside only through deck tools, and its pushes carry no text of its data; its
scripts still reach the manifest's `domains.read` and the package registries, so keep such a script's domains to
what it must reach. Removing the flag later is a right. An agent
that must work in a folder of the owner (a code repo, notes) gets `"folder": true` in `executor` (or the folder's
name when the project has several): it works there instead of its own folder, with the repo's CLAUDE.md or
AGENTS.md, and a second agent in the same folder waits. Only the owner adds folders (the Settings of the
project → "Add folder"); a code repo is the code-project skill. List the
skills it needs in `executor.skills`; a skill may not grant tools (`allowed-tools`, `hooks`) — rights come
from deck.

The owner picks the models of each agent command in settings (`assignment_set`); the manifest only says
what the command needs and suggests one. `requires` lists capabilities, and usually only two matter:
`no_cloud` for personal data (finance, health) that must stay on the owner's hardware, and `sandbox` when the
agent must run shell commands or write files (a provider in auto mode, or a model the owner runs in an ACP agent
inside deck's sandbox). The two go together:
a local model in an ACP agent has a shell and stays on the owner's hardware. `web` decides only which provider may run the command: an agent on Claude Code or Codex
searches with its own web whether or not it is required; a model in an ACP agent (a free or local one) has deck's
`web_search` (the search service of deck's settings, SearXNG) and `web_fetch` (pages of `domains.read` as text, any
with `"*"`). Require it when the agent's result is wrong without its own research (reading
changelogs or news it cannot know in advance); the prompt of such a command names `web_search` and `web_fetch`, so
a model in an ACP agent knows to use them. Leave it out when a script brings the pages the agent judges, which is
cheaper. Fetching known pages belongs in a script with `domains.read`. Repeatable mechanics still belong in a script: it is cheaper,
runs the same way every time and works with any provider. Do not require `tools` or `structured_output`: every provider has deck's tools and hands in the result, and a
requirement a local model lacks keeps routine work off it. `commands_list` gives each agent command the
providers that fit it. `provider` suggests a provider by
name — routine judgement suits a local model in an ACP agent (the name the owner gave it, from
`models_list`), heavy reasoning Claude Code or Codex. Write the
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
`allowed_external` and questions the agent asks. Before hand-over,
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
2. `project_import` with the absolute folder path creates a **draft**; the folder is in your data (the master's)
   or in a folder the owner gave a project (its settings, Access); any other folder only the owner imports, in
   Settings → Modules. A refused import comes back with the
   reason and leaves nothing behind; fix the folder and import again. Once imported, change the draft with
   `manifest_save` and `document_save` (a second import of the same name is refused), and keep the folder
   in step. Send only what changes: for the manifest and for a document `old` (parts of the current text, each
   found exactly once) and `new` (what replaces each); the whole text only for a new one or a rewrite. The answer of `manifest_save`
   lists in `screens` each block that fails on the records there are now (a column that breaks on a record
   without a field, a missing custom document): fix them before the owner opens the screen.
3. `command_try` runs a command as a trial: everything that is not internal is declined, and a call of an HTTP
   or MCP tool waits for the owner's yes on that one call; the commands that run `after` it are tried too,
   unless `only` asks for this command alone. Try in a draft. Until the owner enables a draft it
   has none of the rights its manifest asks for: its tools ask the owner on every call, its scripts reach only
   package registries, its `domains` do nothing; its `reads` work for reading only (a project with
   `no_network` stays closed to a trial). To try a change of an enabled module (a new command, one that waits for the owner's
   button), import its folder as a draft copy under another name, try there, then save the change to the
   module and tell the owner the copy can be deleted: a trial in the enabled module itself writes real
   records beside its own runs, so it takes `live` and is refused while a real run of the command
   is going on. It runs as you, the master: leave `callers` out (everyone) unless a command must be closed to
   someone, and keep `master` in a list you give. The `after` triggers it sets off run as trials too, so a whole chain can be tried from
   its first command. A trial is not a sandbox: internal writes (records, documents) are real and stay
   in the draft, and so is what scripts write to `$DECK_DATA`, which you cannot reach: keep what a module
   remembers (seen items, cursors) in records when trials must be undone. Before the hand-over, delete what
   the trials left that the owner should not start with: `record_delete` (you, in a draft; in an enabled project what your own runs wrote, with an undo of its data for the owner in
   the feed, and when it takes a file, folder or process you did not make it waits for the owner's yes; the rest only the owner, by a button);
   say in the hand-over what you kept; lines a trial sent with `notify` stay in the feed. `process_get` gives
   the state and result; `process_log` shows the events of a process, also after it ended,
   and a local model may think for many minutes before its first one: wait with
   `process_get` and `wait` in the foreground (up to 30 minutes on Claude Code and Codex). `processes_list`
   with `command` and `brief` and `records_list` with `where` (an expression, as in a source) and `fields` keep long lists short. Time the trial on the provider the command will run on and set
   `timeout_minutes`, batch sizes and the schedule by it. A trial that ends `done` is not yet a proof: check
   that the chain gave what the owner will see (records, documents). Check `records_list` and `health_list` after a save that adds programs or
   MCP servers: a missing program or a failed server shows there with its error.
4. A draft never runs on its schedule. Only the owner enables it.

## 8. Moving modules and skills

- **Export a module** into a new or empty folder: `documents_list` of the project, then `document_get` of every
  document of kind manifest, prompt, script, skill, custom and doc that has no `owner_kind`, written to the
  folder under its name. Artifacts and documents of records are data, not the module; leave them
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

Ask only for what the module needs. `allowed_external`, domains, secrets, `models`, milder classes
of tools, `reads` of other projects and removing `no_network` are rights: in a draft they are part of what the owner enables; in an
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
