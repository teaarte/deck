# What changed in deck for modules

Newest last. Remember in your memory the last line you read; after an update of deck read only the lines after it
and bring the modules in line.

- 2026-10-31, format 14: deck's commands are named `<entity>_<verb>` — `_get` one, `<plural>_list` many, `_delete`,
  `_revoke`; a plural only before `_list` (`record_upsert`, `setting_set`). There are no aliases: the migration
  rewrote `calls` of every stored manifest and `deck.<old>` in the documents of the master; `deck.command_describe`
  gives the input of any command.
- 2026-11-01: a secret is of its module: `$secret.x` reads only the secrets of the module that names it, the keys of
  connections are apart; `from_block` takes only fields of the input, never the fields deck reserves (allows,
  retry_key).
- 2026-11-01, format 15: every foreign program — script, MCP server, ACP agent, Claude Code, Codex — runs in a box
  of its module; `no_network` and `requires.local_browser` are gone. The network of a module is its rights:
  `domains.read` are the hosts its programs reach (`"*"` every public one, never the local network); without
  domains its agents reach only their provider and deck. A module that needs the web names its hosts in
  `domains.read`.
- 2026-11-01: the master reads the public web: its manifest has `domains.read: ["*"]`; no `act`, so a click or a
  form in the browser still waits for the owner.
- 2026-11-01: pushes are never private by a module any more; the owner's setting `push_text` (`full`, `hidden`)
  hides the text of every push, which then says only the module it is of.
- 2026-11-02: a result an agent hands in with `result_submit` is kept by its process at once, and the steps `after`
  its command start then, not when the session ends. A call for the same record after that starts a run of its own
  instead of joining. A process stopped, failed or left by its model after it handed in its result ends done with it;
  a second result of the same process is refused; `process_get` gives `submitted_at`.
- 2026-11-02: deck tells the master of incidents around a process — `result_running`, `result_not_done`,
  `after_not_started` — by the trigger `on: incident` of the master (`on_incident`, skill `incident-recovery`); deck
  mends none itself.
- 2026-11-02: on a server, in deck's image, every program of a module runs in its box as elsewhere: it reaches only
  the hosts of its `domains.read`, not the network of the container, and sees no folder of another module; Claude
  Code and Codex keep a home per project in deck's folder, with the one login.
- 2026-11-02: `models_list` gives the models that could do each kind of work once, in `choices` (`role`, `models`,
  `groups`); a row of `assignments` holds only `models`, `using` and `why`, no `choices` or `price`.
- 2026-11-02, format 16: a command of a module runs one at a time unless it says `"runs": "many"`: a call while a
  run goes on waits as a run of its own. The migration gave `"runs": "many"` to every command whose real runs went
  side by side before, so nothing slowed; every other command without `runs` is one at a time now. Check each such
  command of the modules (skill module-conventions): `many` only where runs write nothing in common, must answer at
  once while another runs, or call themselves.
- 2026-11-02: `commands_list` is brief: name, title, description, project, files, executor, tool, owner_only, form,
  form_params; the model of a command, its `choices`, price and schedule come from `command_describe` or by `fields`.
  A name in `fields` that no field of the result has is refused with the fields there are, of every tool with
  `fields`.
- 2026-11-02: `domains.read` is network, not only reading: deck's door sees the host, not the method, so a program
  may send to a host it may read. Acting on a site without the owner is only `domains.act` in the browser. A
  service on the owner's machine or network is named by IP:port, never by a name (Codex on a local model too).
- 2026-11-02: a read of deck says its topics in the header `Deck-Topics` of its answer; `menu_get` is a read and has
  no field `topics`. A button that names a `verb` comes with its `label` in the owner's language and, without a label
  of its own, its `icon`: data carries no word for the client to translate. Buttons deck gives in data: `use` of
  `themes_list` rows and of `theme_get` (a theme not in use), `remove` of `push_devices_list` rows, `save` of
  `layout_get`, `draw` of the projects of `menu_get`, `transcribe` of `chat_get`; `process_asks_list` gives the
  `result` of a run once done; `theme_use` answers with the theme's `accent`; `push_key_get` is a read.
- 2026-11-02: `secrets_list` gives `project`, `named` and `changed_at` of each secret, and also the secrets set that
  the manifest does not name (yet), `named: false`; without `project` it lists every module's. Values never.
- 2026-11-02: the thirds of a line a widget is laid in are said once, in the description of `widgets` in
  `deck.manifest_format_get`; the widgets skill names it instead of numbers. A read of deck answers in the language
  of the call (its `lang`, else the client's); nested reads need no `lang` of their own.
- 2026-11-03: the owner signs in by devices: a browser pairs by a one-time link (`deck.pair_link`) and its passkey,
  then calls with a cookie; `devices_list`, `device_rename`, `device_revoke`; there is no owner token. An automation
  token calls only in the header `Authorization` (never in an address; ten wrong ones from an address and it waits);
  the topic of `tokens_list` is `devices`. A page deck opens itself, such as `screen_capture`, reads with a cookie of
  the caller that only reads. `push_subscribe` is of the device the call comes from and takes no `device`.
- 2026-11-03: `push_devices_list` is gone: `devices_list` says `push` of each device and gives its `stop_push` button.
  `pair_link` answers with `qr`, a PNG of the first link (`contentMediaType: image/png`), and pairs a browser only at
  an https address of deck. `layout_get` and `layout_set` take no `device`: the tiles are of the device of the call. A
  page deck opens itself, such as `screen_capture`, reads with a lent token in the header, no cookie.
- 2026-11-04: a passkey and a device are apart: a passkey synced to many browsers signs each in as a device of its
  own, named by its browser. `devices_list` says `passkey` (the name of the passkey a browser signed in with) and
  `passkey_id`; `passkeys_list`, `passkey_rename`, `passkey_revoke` (signs out every device of the passkey). A revoked
  device signs in again by its passkey only as a new device.
- 2026-11-05: a connection may be served by a node, a machine of the owner joined to deck by `deck -join`: its models
  listen on that machine's loopback and deck calls them over the node's connection. `models_list` says `node` (its
  name) and `online` of each connection; while the node is off a call of its model goes to the next model of the
  fallbacks. A module calls such a model as any other, by its name or role: no manifest names a node.
- 2026-11-06: an agent may run on a node: `claude-code@Mac`, `<model of a CLI>@Mac` or `<model in an ACP agent>@Mac`
  is the same agent on the node's machine, on its sign-in, a provider of its own in `models_list` (`node` names the
  node) while the node is online and runs it. Assign it to agent commands as any other, with fallbacks; a manifest
  never names a node. Its turn works in the node's copy of the turn's folders (run, results, data), and the files it
  changed come back to deck at the end of the turn; a project folder of deck's machine stays out of its reach, so
  such a turn goes to the next agent. A node gone in the middle of a turn is waited for a few minutes, then the work
  goes to the next agent of the assignment. A node serves only the ports and agents its owner allows there.
- 2026-11-07: a project folder can be on a node (`folder_add` with `node`; `folders_list` gives `node` and `repo`): an
  agent command in such a folder runs on the agent of that node in the folder itself, a git repository may have a
  clone on each machine under one name; the scripts of a module whose folders are on a node run on that node and read
  them there, the others on deck, without a field of the manifest. Data, records and files stay on deck; what a script
  on a node changed in its run folder and the data of the module comes back at its end. The node allows a folder by
  its path (`--allow ~/lab/data`); a folder off its list is refused with what to add.
- 2026-11-07: a block may stand `"collapsed": true` — folded under its title, read once the owner opens it — for what
  is seldom needed below what the owner acts on. A line of a list takes `logo` (words naming a model, its maker or
  provider: the client draws its mark in place of the icon, else the first letter), a column the format `logo`.
  Buttons of deck's verbs wear an icon each.
- 2026-11-07 (written 2026-11-08; a summary of what modules see now, some of it older): deck runs in Docker on the owner's Mac as on a server — its programs
  are those of deck's image (Claude Code, Codex, Goose, uv, python3, node, git, chromium); a program a module names
  that is not in the image is a failure of its command in health, not something to install with brew. A text model of
  an API runs agent commands only in an ACP agent, Goose in deck's image: `model_set` with `agent: goose`
  (`check_minutes` gives a slow one more time for its self-check, `context` and `tpm` are the limits past which a
  request goes to the next model). `assignment_set` takes `model`, one model that goes first: the models in force there
  before stay after it as fallbacks; `models` still sets the whole list. `deck.model_replace` (the owner's only) puts a
  model in place of another in every work of a kind it does first. A row of a model in `health_list` gives `places`,
  the work it does. `command_try` in an enabled project runs only with `live`; else try the change in a draft copy.
- 2026-11-08: a module exports with its data (`project_export` with `data`: records, memory, the data folder) and an
  import brings it back; `project_reset` empties its data after a snapshot. A widget whose blocks are all empty gives
  way to the cover of its module. An empty block below another of its place stays out of sight — say why a screen is
  empty in its first block. A column of a table takes `icon` (an expression of the row's icon), a chart `details`
  (the text of a point, beside its values). `folder_add` with `replace` changes the path of a folder in place;
  `commands_list` gives `document`, the file of a command's script or prompt, by its fields. The theme token
  `widget-card` colors a card that shows its module's view without a cover; with a cover the view stands on a veil
  over it; `widget-row-gap` (the space between the lines of a widget) and the class `widget-row` (a line of a widget,
  `.widget-row` in the `css` of a theme) shape its lines.
- 2026-11-08: `version_get` of a deck built from source says `dev+<commit>` and `commit`; a release says its tag.
- 2026-11-08: a `file` field takes the name a call answered with as it is (`tts-….mp3` from `deck.model_call`, a
  tool, a screen shot), the same file as `results/<name>` of the calling process.
- 2026-11-08: deck's image carries `ffmpeg` and `unzip` beside `python3`, `node`, `uv`, `bash`, `git`, `curl`,
  `chromium` and Goose (the list is in the skill of modules); a script names them in `programs` all the same. A program
  missing from the image says so in health — the owner updates the image; elsewhere the `install` of the manifest stays,
  a plain `brew install <formula>` read as `apt-get install <formula>` on Linux. The scripts of a module whose folders
  are on a node are checked by the node when they run, not by deck's machine.
- 2026-11-08: `assignment_set` takes `assignments`, a JSON list of `{role, project, command, models or model}`, in
  place of its own fields: several assignments wait for one button of the owner, each checked as its own call. A call
  that waits for the owner answers `link`, and `asks_list` gives each ask its own `link` (Today with that ask first),
  not the screen of all. A process that waits for its model goes on, told what it did, on a model its assignment now
  puts before that one, once it takes turns; behind its own model it waits in its session. `health_list` gives a
  model `fallbacks` beside `places`: the work it stands in while another model does it, after it or while it is down.
- 2026-11-08: a model on a node (`codex@Mac`, a model of a CLI or of an ACP agent `@Mac`) is a model deck has while
  that node is paired and, while it is connected, runs that agent: health no longer says "deck has no such model" of
  it, and an assignment of an agent the node does not run is refused with what to allow on it; work whose first
  model is on a node away says `its node Mac is offline`. The row of a node names its connections
  in quotes, `it serves the connection "media"`, `the connection "local" does not answer on its machine`; the step of
  a self-check after resume is `turn after resume`. `project_readiness_get` with `lang` gives the rights in that
  language. A question to the owner without text is refused to the agent; a process parked on a frozen call shows
  the call as what it waits for.
- 2026-11-08: a picker of models offers only those of its work and says each in short. `models_list` gives each model
  `roles` (the kinds of work it can take: `agent` and `widget` for a model that runs agent turns, else its kind),
  `note` (price, speed of its last self-check, tokens a request holds) and `logo` (words naming its provider, then
  the model). The work of a command gives `choice_notes` and `choice_logos` beside `choices`. A button with `options`
  takes `notes` and `logos` (lists in the order of options) and a choice of a form `note`, `logo` and `icon` (each an
  expression over its item): the client shows the mark, the label and the note of each option, such as
  `"choices": {"model": {"source": {"from": "deck.models_list", "rows": "models", "where": "'tts' in roles"}, "value":
  "name", "note": "note ?? nil", "logo": "logo"}}`. A column of a table takes `logo` (an expression of words naming
  a model or its provider, such as `connection + ' ' + model`): the cell wears that mark before its value; the work of
  a command gives `logo` of its model, provider first.
- 2026-11-08: a topic of `chat_topics_list` gives `tokens` (what its replies and the work they started spent),
  `replied_on` (the model of its last reply) and `logo` (the words of that model's mark); with `module` it gives only
  the topics about that module (`-` for those about none), and `modules` names the modules of all topics (`project`, `-` for none) with `last`, the newest topic of each.
- 2026-11-08: a memory fact may be `type: lesson` — a conclusion of deck's agents, not the owner's words — with
  `source` (required: `process:<id>`, `topic:<id>` or `incident:<process id>.<kind>`, comma-separated), `command` (a
  command of that project) and `expires`; an agent writes lessons in its own project's memory, the master in any.
  Index lines of lessons go under `## Lessons` of `memory/MEMORY.md`: only the first 40 reach a prompt, a save past
  them warns; `document_get` of a memory document that would no longer save answers `warning` with why (memory skill).
- 2026-11-08: the first prompt of a run of an agent command carries, beside the memory index, the lessons of that
  command (`type: lesson`, `command: <it>`, not expired) of the memory of its project, the newest twelve within 4000
  bytes, as data; a run that may save documents is asked to write or update a lesson when it taught something.
  `on_error` and `on_incident` write lessons of the command that failed in its project's memory.
- 2026-11-08: `deck.memory_search` finds words (any of them, in any form: a part of a word
  without its Russian ending, ё as е; a word under three letters, such as Go or v2, as a word of its own) in memory,
  the summaries of the master's topics, skills and finished processes (command, error, the start of the result), as
  far as the caller reads them; `kinds`, `project`, `limit`; each hit gives `kind`, `name`, `document` and `revision`
  or `process`, `snippet` and `rank` (lower is better).
- 2026-11-08: `deck.episodes_list` (the owner and the master) gives what happened from `since` until `until` (all so far
  without one), oldest first: `processes` that finished (not trials nor done with nothing: `state`, `error_type`,
  `error` cut, `provider`, `models`, `duration_ms`, `topic`), `incidents` (`process`, `kind`, `at`), the owner's
  `messages` in chat topics and the `summaries` of topics that changed, `limit` a part (200, up to 1000); `more` names
  the parts cut: call again with `since` the last `finished_at` or `at` of that part for the rest (rows of that very
  time may repeat); `project` narrows the processes and incidents. A process stays listed for good, its full log a week.
- 2026-11-08: the master's weekly `memory` is gone; its nightly `sleep` (`30 3 * * *`) keeps memory by the episodes
  since the last one: writes, confirms and retires lessons with their source, proposes changes of the owner's facts
  and of skills (the owner's yes saves them), tidies indexes;
  the migration gives sleep the pause and the models of `memory`. A command's `provider` may be `"free"`: with no
  assignment, the agent models marked free and then the rest are its chain, as an assignment is.
- 2026-11-08: `deck.document_propose` (master, owner) proposes the whole new `text` of a skill file (`name` as
  `document_get` names it) or of a memory fact that is not a lesson (`memory/<fact>.md`; without `project` the owner's),
  or with `delete: true` deleting such a fact; `reason` names the lessons it comes from or quotes the episode with its
  source; `if_revision` is the revision it was written from. The owner sees the reason and the whole diff and makes
  the change with one button; without a yes nothing changes. Refused at once: a document that is not there, a lesson
  (save it with `document_save`), the index, a text that fails the checks of its kind, a document changed since
  `if_revision`, a diff past 2000 bytes (propose a smaller change). A text in force already is done without asking; a
  yes to a document changed since does not apply; a button's `allows` does not stand for it, as for calls that spend
  money.
- 2026-11-08: a lesson may say `confirmed` (how many times its situation came again, a whole number from 1) and
  `last_confirmed` (a date); when the same thing happens again raise them instead of writing a new lesson, and merge
  lessons that contradict each other (memory skill). A turn gets at most ten lessons of its command, the most
  confirmed first, then those confirmed or written last.
- 2026-11-08: the memory a turn loads — the owner's index, the index of its project and the lessons of its command —
  stays within 12 KB in that order, each part present with at least 3 KB; a part cut says after its data that the rest
  is found with `memory_search` or `document_get`.
- 2026-11-08: a memory document counts its reads: a lesson that reached a turn, a fact an agent or the master read
  with `document_get` or found with `memory_search` (not the index, not the owner's reads); `documents_list` and the
  hits of `memory_search` give `reads` and `last_read_at` — a signal of what to raise or drop in the index.
- 2026-11-09: a run no one started by hand — of a schedule or an event, or one such a run started; a reply to the
  owner's message is not — saves and deletes in memory only lessons, the index `memory/MEMORY.md` and facts whose
  `expires` passed; a save or delete of another fact is refused ("propose it with document_propose; the owner
  decides"). `document_propose` stays the master's and the owner's.
- 2026-11-09: `deck.memory_map` (owner and master; it was `memory_overview`) gives the parts of deck's memory:
  `projects` (facts, lessons, index lines of 150, last change, the index document), `lessons` (document, project,
  command, source, confirmed, reads), `nodes` (the owner's memory, modules, summaries of topics, skills, sleep,
  review, on_error, on_incident with models, schedule and last run, the agents' own memory: off, repeated failures), `repeats` (the
  commands that failed the same way more than once in `days`, 30 by default, trials and runs handed on aside) and
  `picture`, a drawing of what feeds what in the master's data folder, in the language of the workspace. Settings has
  a tab Memory of it. A path of a data folder in `item.file` of a media block may end with `?v=<version>`: the file
  is the same, its address changes with the version, so the client reads it again.
- 2026-11-09: a call of a project's command that is not an agent (script, tool, model) from an agent's or the
  master's turn waits up to 15 seconds and gives `{process, state, result}` (or `error`, `error_type`) when it ended,
  `{process, state, ask, question}` when it waits for the owner, else `{process, state}` as before; agent commands
  give `{process}` at once. `process_get` without `wait` of an open process from a turn gives `hint`, how to wait; the
  same look again without `wait` waits up to a minute itself. The third same read in a row with the same answer gets
  one warning after its answer; the turn stops as a loop only if the reads go on.
- 2026-11-09: a turn that calls an agent command while a process of it goes in the same topic (input `topic`) gets
  that process back (`running`, `note`) instead of a second one: the same input as it is, another input given to it
  as a message it takes into its run; a command that runs one at a time gives the one going for the same input. A
  retry key of its own (retry_key) starts another beside it. The owner's and triggers' calls, and a script's, are as
  before.
- 2026-11-09: `document_save` with a whole `text` and `manifest_save` with a whole `manifest`, from a turn, without
  `if_revision`, are refused as a conflict when others saved the document after the revision the turn read: the
  refusal names each revision after it, who saved it and why; read it again and change it on top (old and new parts,
  or the whole text with `if_revision`).
- 2026-11-09: an agent turn that ends without a result (none handed in, none by its schema) while processes it
  started still work (running or waiting for a slot or its turn) fails as `agent.left` (whose: model) instead of
  done; the master's reply, on_error and on_incident are as before. A work of
  a topic handed on to the next model tells the topic `restarted as #N on <model>`.
- 2026-11-09: an agent command that changes records, documents or files declares `changes` in its result schema,
  an array of strings, one short line each: what changed, where and why; a review of a module checks it, and the
  master compares it with the task it gave. deck does not check it.
- 2026-11-09: any module may have one trigger `{"on": "ask", "command": X}`: X starts once when a confirmation a
  process of this module asked the owner for closes, with the input `ask` (id), `answer` (`yes`, `no` or `expired`)
  and `process` (the one that asked), so its input declares those fields; a module never hears the asks of others.
  A module that polled for the owner's answers on a schedule takes this trigger instead. X is not external; an ask
  deck closed itself (what it acts on changed) is `expired`, one closed while the module was paused is not heard.
- 2026-11-09: `deck.model_delete` may stand in `allowed_external`: then a module removes without the owner a model its
  own call of `deck.model_set` added, free and named in no assignment, and the owner sees it among the changes made
  without them with an undo that brings the model back; any other removal by a module waits for the owner. deck no
  longer checks by itself a model in an agent that no assignment names; `deck.provider_check` still does.
- 2026-11-09: an agent of a module calls `deck.data_read` too, on its own project only; `deck.data_read` of a PNG,
  JPEG, GIF or WebP gives the picture to an agent as an image beside its answer (with `fields` too), whose `image`
  is `{type, url, width, height}`; a picture over 3.75 MB or 8000 pixels a side is refused with its size and the
  limit, a PDF is refused (a script gets its text or its pages as pictures into the data folder first).
- 2026-11-09: `deck.file_delete` takes `project` and `path` instead of `file` for a file of the data folder of a
  project (not a folder, not through a link); the master and a process of the project delete the files of its records
  and of its data folder, not only those of their own run, and the owner sees each among the changes made without
  them with an undo for a day (`file_restore` with `owner_kind` `data` and `path`, never over a file there again), a
  file of the data folder in a draft too. The master names `project`; another project is refused.
- 2026-11-09: `deck.models_list` marks a model `spare: true` for a module that may remove it without the owner by
  `deck.model_delete`: its own call added it, it is free and no assignment of any project names it.
- 2026-11-09: the starter module free-models lists `deck.model_delete` in `allowed_external`: its new command `prune`
  (daily) removes a model `spare` for it once it has been broken for 7 days (deck's health is error, or its last
  failed test failed the model itself) and passed no test since; the owner sees it among the changes made without
  them with an undo.
- 2026-11-09: each reply turn of the master gets after the messages, as data, the projects (name, state, the first
  line of the description), the work of its topic still going with the asks it waits on, and the revision of the
  topic summary with the messages after it: `projects_list`, `processes_list` and `document_get` of the summary only
  for more. The skills of an agent's session keep their path from turn to turn while they do not change.
- 2026-11-09: a number field of a command input (`integer` or `number`) with both `minimum` and `maximum` is a slider in
  every form of deck, its exact value typed beside its label; `multipleOf` is its step, numbers of `examples` are
  marks under the track that pull the thumb near them. Give a bounded number, such as a price, a term or a count,
  both bounds and a few examples.
- 2026-11-09: a reply of the master in a chat topic shows one block under its text with `deck.chat_block_show`:
  options to pick, a form (a bounded number slides), a table, a chart or a card, its data in place, by the schema
  `shown` of deck's catalog; a wrong block is refused with why. The owner's answer to options or a form comes as the
  owner's next message of the topic, and the block keeps it.
- 2026-11-09: `logo` of a model in `deck.models_list` (and `choice_logos`, the `logo` of work) names the model itself
  first — its id at the provider, else its name — then its connection and agent, so its mark is the maker's, not the
  provider's, and the provider's only for a maker deck has no mark of; the provider stands in `provider` (and starts
  `choice_groups`), whose words give its mark. A form's `choices` take `group`, the heading a value
  stands under, such as `"group": "provider"` of models: the picker groups the values by it.
- 2026-11-09: the built-in Tailscale (`-tailscale`, `DECK_TAILSCALE`) still runs but goes away in a coming release; a
  deck started with it has the health row `tailscale:builtin` (warning) saying how to move to Tailscale on the host
  (`tailscale serve`, `-public`/`DECK_DOMAIN`, pair the phone again). Off it, the rows `tailscale` and
  `tailscale:builtin` go at the start.
- 2026-11-10: a question to the owner is one ask of deck: options or a form a reply shows with `deck.chat_block_show`
  waits for the owner in the topic and in "waiting for you" (`deck.asks_list`, a question whose `payload.questions[0]`
  has `options` or `form`), answered in either with `deck.ask_answer` (a form's answer is its fields as a JSON
  object); the answer comes as the owner's next message of the topic. The chat's own answer command is gone. The
  owner's own message in the topic closes its open options and forms. A reply asks by these blocks, not by its
  question tool.
- 2026-11-10: `version_get` of a build from source that does not know its commit gives `unknown`, why and how to
  build one that knows it, beside `version` `dev`; the image `deck:local` of `install.sh` knows its commit in a git
  worktree too.
- 2026-11-10: `manifest_check`, `manifest_save` and an import warn of an agent command that may change records,
  documents or files (it works in a folder, or its `calls` are all commands or name `record_upsert`, `record_update`,
  `record_delete`, `document_save`, `document_delete`, `file_upload` or `file_delete`) whose result schema has no
  `changes`; the save and the import still take it.
- 2026-11-10: a model has `tools`, the most tools one request of it takes (`model_set` with `tools`, `models_list`);
  deck learns it from an answer of its API that says so, as Groq does past 128. A request with more tools goes to the
  next model, and agent work whose turns have more is not offered the model: the work of `models_list` gives
  `left_out`, why such models are not among its `choices`.
