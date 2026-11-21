# What changed in deck for modules

Newest last. Remember in your memory the last line you read; after an update of deck read only the lines after it
and bring the modules in line. Since 2026-11-10 each line ends with `Modules:` — what to look for in a manifest, its
scripts and prompts and what to put instead — or `Modules: nothing.`

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
  without its Russian ending, the Russian yo as ye; a word under three letters, such as Go or v2, as a word of its own) in memory,
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
  Modules: nothing; the master's own replies ask by these blocks.
- 2026-11-10: `version_get` of a build from source that does not know its commit gives `unknown`, why and how to
  build one that knows it, beside `version` `dev`; the image `deck:local` of `install.sh` knows its commit in a git
  worktree too.
  Modules: nothing.
- 2026-11-10: `manifest_check`, `manifest_save` and an import warn of an agent command that may change records,
  documents or files (it works in a folder, or its `calls` are all commands or name `record_upsert`, `record_update`,
  `record_delete`, `document_save`, `document_delete`, `file_upload` or `file_delete`) whose result schema has no
  `changes`; the save and the import still take it.
  Modules: an agent command that may change records, documents or files gets `changes` in its `result` schema;
  `manifest_check` names each one without.
- 2026-11-10: a model has `tools`, the most tools one request of it takes (`model_set` with `tools`, `models_list`);
  deck learns it from an answer of its API that says so, as Groq does past 128. A request with more tools goes to the
  next model, and agent work whose turns have more is not offered the model: the work of `models_list` gives
  `left_out`, why such models are not among its `choices`.
  Modules: nothing.
- 2026-11-10: `health_list` names the kinds of its rows by what deck writes: `kind` takes `node` and `https`, and no
  longer `browser`; `status` no longer lists `missing`, which no health row has.
  Modules: a screen or script that filters `health_list` by `kind` `browser` or `status` `missing`: drop that filter.
- 2026-11-11: One wait is one question. A question of a process that names one of its own requests still waiting for
  the owner (`ask 315`, the same in Russian, its link) is refused: deck brings the request's outcome itself, end the turn or
  go on. A question asked again with the same text while it is open waits on the open one. A held call that sets every
  place of an earlier held call of the same process and command (`assignment_set`) replaces it: the earlier closes
  answered `replaced:<id>` (its caller hears so, `on: ask` hears `expired`), and the owner sees `Replaces request
  <id>` on the later.
  Modules: a prompt that tells an agent to ask the owner about its own request still waiting, or to ask the same
  question again: drop it; deck brings the outcome.
- 2026-11-11: A reply of the master reads the topic it answers as the owner sees it: `chat_get` and `chat_turn_get`
  with `topic` set to that topic, or without one, give its messages, the work over them and what waits, and
  `screen_capture` of `#/deck/chat` draws it. Any other topic is refused; the list of topics stays the owner's.
  Modules: nothing.
- 2026-11-11: when deck ships a new text of a document you edited (a prompt, a skill), a line you added beside a line
  deck changed stays; only the same lines changed by both are a conflict with a note.
  Modules: nothing.
- 2026-11-11: A turn of any agent that repeats the same call of any tool with the same answer, its input differing at
  most in numbers the answer echoes, is warned at the third call (an agent that takes live input hears it, the journal
  shows it) and stopped as a loop at the sixth (`agent.loop`, in health and the feed). The key of a box's door and a
  script's token are masked (`[secret]`) in everything its programs print.
  Modules: nothing; the line of 2026-11-11 on one guard of loops below is what holds.
- 2026-11-11: files the owner attaches to a chat message or to a message of a process are in the box of the turn at the
  path deck names with the message, in an input folder the turn reads and does not write; the paths named in earlier
  turns of the same session and in the process a turn continues stay readable. A turn on a node gets them as a turn
  starts there: files given while it runs, or with an answer to its question, come with its next turn.
  Modules: nothing.
- 2026-11-11: one language rule. A chat topic has a language (`lang`): the master's reply marks the one the owner's
  latest message is written in with `deck.chat_topic_lang_set` when it differs from the one deck gives, and the last
  message decides. What comes of a topic — the reply, works and commands started from it, their requests and
  questions, deck's words of its work in the chat, the feed and pushes of those processes — is in its language; without
  a topic, in the language of the settings. Every agent's prompt says the language deck gives; prompts and skills keep
  no languages of their own.
  Modules: a prompt or skill that names a language to write in ("answer in Russian"): drop it, deck gives the
  language; a text a script writes is English with its translation in `i18n`.
- 2026-11-11: a request is plain for the owner. A call that may wait for the owner's yes takes `why` (a field every
  command that is not internal now has; any other command takes it and drops it; `reason` of deck's own commands, such
  as document_propose and a manifest_save that widens rights, stands for it): one short sentence in the language deck
  gives; an agent's call without it is refused at once and nothing is asked. The same call asked again with other
  words of why is the request already open. On top of a request deck writes what changes,
  a line a place in the owner's words (`change` of asks_list); JSON, diffs and command names go under the details.
  A call that changes nothing — the same models, a text already in force — makes no request and answers
  `unchanged`. deck's message of a work that waits for a request says its why.
  Modules: a prompt of an agent command whose calls may wait for the owner's yes tells the agent to pass `why`, one
  short sentence; without it the call is refused.
- 2026-11-11: a process paused on a model's usage limit (`rate_limited`) and the model's health wait until the reset
  the model API told: Retry-After, X-RateLimit-Reset (OpenRouter puts it in the metadata of its error, in unix
  milliseconds), a reset as a duration (Groq), Gemini's retryDelay; without one, a limit a minute waits a minute, not an
  hour; a limit a day waits until 00:00 UTC. Work whose assignment has another model still goes on there at once.
  Modules: nothing.
- 2026-11-11: the tools of `records_list`, `record_get`, `record_upsert`, `record_update`, `document_get`,
  `document_save` and `process_get` tell an agent the shape of their answer and an example input where the schema
  leaves it open; the tool of a module's command adds its `result` schema without titles and descriptions (past 500
  bytes only its fields and their types) and an input of the `examples` of its parameters when they fill every
  required one. `manifest_check`, `manifest_save` and an import warn of an agent command whose `calls` name a command of
  the module (`<project>.<name>`) an agent may call without a `result` schema; the save and the import still take it. A prompt of an
  agent command says what each of its calls answers and gives one to three inputs (module-conventions).
  Modules: a command of the module an agent calls gets a `result` schema (`manifest_check` names each one without),
  and the prompt of an agent command says what each call answers with one to three inputs.
- 2026-11-11: the master has no `domains`: in its box its shell reaches only its model and deck (on a Mac without
  Docker Claude Code and Codex still run outside the box); it searches with `web_search` and reads pages with
  `web_fetch`. Both write a line of the gateway journal (`params` of the owner's `calls_list`): `web_fetch` the
  address asked and, after a redirect, the one read (`read`); `web_search` the query, the service and the address of
  each result. A widening of `domains.read` reads "reaches and may send data to: <host>": the door
  sees the host, not the method.
  Modules: nothing.
- 2026-11-11: a process waits in one state, `waiting`, instead of seven (for an answer, a login, a usage limit, a
  provider, a slot, a folder, the run before): `process_get` and
  `processes_list` give its `wait` — `reason` (`answer`, `sign_in`, `limit`, `provider`, `slot`, `folder`, `run`),
  `on` (the ask, provider, folder or run), `until` (its term) and `silence` (what comes at the term: `retry`, `go_on` or
  `fail:<type>`); `paused_until` is gone, its time is `wait.until`. A question an agent asks waits as long as a
  confirmation (`confirm_hours`, a day): then the agent is told the owner did not answer and finishes with what it has.
  A lost login waits a week, a red provider a day, and then the process fails (`model.login`, `model.unavailable`); a
  turn without deck's tools waits for its provider an hour (`deck.tools`) instead of three tries.
  Modules: a screen, script or prompt that reads `paused_until` or a waiting state other than `waiting`: read `state`
  `waiting` and its `wait` (`reason`, `on`, `until`).
- 2026-11-11: one guard of loops: the same call of a tool with the same answer six times in a row stops the turn as
  `agent.loop`, as before, but nothing warns at the third any more — neither the agent nor the answer of a read;
  `process_get` without `wait` answers at once each time instead of waiting a minute on its own after the first look,
  and its `hint` no longer counts the seconds, so six such looks in a row stop the turn as a loop; a script failing
  three times in a row no longer writes a warning of its own (the failed list counts it, the master hears it as
  before).
  Modules: a prompt that waits for a process by calling `process_get` again and again: call it once with `wait`.
- 2026-11-11: `"*"` and any name in `domains` reach only public addresses by the IANA special-purpose registries:
  `0.0.0.0/8` (OrbStack's `host.docker.internal` is 0.250.250.254), `198.18.0.0/15` (OrbStack's machines),
  `192.0.0.0/24`, the documentation ranges, `240.0.0.0/4`, Teredo, IPv6 outside `2000::/3` and the same in NAT64,
  6to4 and mapped forms are refused; a service there is reached only by naming its IP, network or IP:port.
  Modules: a `network` right that names a host of a private or special address by its name: name its IP, network or
  IP:port.
- 2026-11-11: a box on Linux needs bubblewrap 0.8 or later: a program in it makes no user namespace of its own
  (`unshare -U` is refused), so it mounts nothing; an older bubblewrap is a red row `sandbox` in health. Chrome
  there runs without its own sandbox (the box is it): a script that starts Chrome itself passes `--no-sandbox`. A
  host with AppArmor loads `deck-box.apparmor` of this version again (README, "Install").
  Modules: a script that starts Chrome itself passes `--no-sandbox`.
- 2026-11-11: a process that waits for a folder names who holds it: `process_get` and `processes_list` give `held_by`
  (the process that holds the folder), `held_ask` (what that one waits for the owner to answer) and `held_until` (the
  term of its wait); its word says what the holder waits for, and Today and the process screen have an Answer button
  to that question. A caller that may not read the holder's module learns only that another module holds the folder.
  Modules: nothing.
- 2026-11-11: deck keeps its base in `state/backups/update-<time>.tar.gz` before a start migrates it; `snapshots_list`
  lists it beside the others. An older deck refuses to start on a base a newer one migrated. The installer takes the
  files beside `compose.yaml` of the release its image is.
  Modules: nothing.
- 2026-11-11: a button an agent or a script puts in the feed with `owner_notify` whose command is not `internal` asks
  the owner first: `feed_list` gives it `confirm: true` and the title of the command as its label, whatever the
  caller set, and the confirmation shows its params (until a later version makes such a press the call of its
  writer). A confirmation of a tool call in `asks_list` gives `change` as the call's fields a line each, where it
  reaches (`to`, `cc`, `url`, …) first, each value cut on its own.
  Modules: nothing; a button of `owner_notify` whose command is not `internal` asks the owner first.
- 2026-11-11: a model's answer deck cannot read (not JSON by the schema, no picture) fails as the model's,
  `model.unavailable` (HTTP 502), no longer as deck's own `deck.internal` (500), and the next model assigned takes the
  call; `cost_usd` of a file is what all the models of the call cost. A video request a service took without a job id,
  or a job that failed, is `model.unavailable` and goes to no other model; one past its ceiling is `timeout`.
  `web_search` failing at its search service, or answering what is not JSON, is `model.unavailable` (502, was 500); a
  page `web_fetch` reads that answers an error status is `network` (502). A failed `tts` keeps the type of its
  failure (a refusal of the service as before, a bad answer `model.unavailable`) with the hint about a voice in its
  details; it is no longer always `input.invalid` (422).
  Modules: a script or prompt that tells failures apart by `deck.internal` (500) of a model's answer or of
  `web_search`, or by `input.invalid` of `tts`: read `model.unavailable` (502) and `network`.
- 2026-11-11: limits of a program's output: a blank line of stderr is no line of the process log, and past 8 MB of
  the log its stderr is dropped with one line that says so (what the agent says and does stays); a picture of the data
  folder past 25 million pixels gets no light copy, a media block shows it itself; the container of deck's compose runs
  at most 4096 processes.
  Modules: nothing.
- 2026-11-11: `file_delete` and `file_restore` move a file of a data folder only inside that folder: a path that
  goes through a link, or a folder swapped for a link meanwhile, is refused (`conflict`, read the folder again);
  nothing of deck's own folder goes into the trash of a project or comes out of it. On macOS a program in a box
  writes inside the folders it may write, but no longer removes, renames or replaces those folders themselves.
  Modules: nothing.
- 2026-11-11: deck reads a file of a folder a box writes only as a regular file inside that folder and up to a limit:
  `.claude/settings*.json` of a repo (1 MB), a screenshot of the browser (32 MB); a link out of the folder, a pipe or a
  device is not read (a snapshot skips it and names it); `project_import` skips such a file as not a regular one.
  Modules: a module folder with links or devices in it: `project_import` skips them; keep regular files only.
- 2026-11-11: in a retelling of a chat topic (a turn after another provider, the messages after a summary) only the
  owner's text stands as written; what deck told of a work and what the master said is one quoted line each, so a
  line of a work's result never reads as a message of the owner.
  Modules: nothing.
- 2026-11-11: a custom block makes no frame by a node either: inserting or adopting any node with an
  `iframe`/`frame`/`object`/`embed` in it is refused, wherever the node was made (an XML document too), and the guard
  calls only the builtins it took before the block ran. The card of a module before enabling says that its custom
  blocks see the data their screens give them and may carry it out of deck. Files shared to deck go only into the
  forms of the share screen, and a page of another site shares nothing.
  Modules: a custom block that inserts an `iframe`, `frame`, `object` or `embed`: draw it without one.
- 2026-11-11: what a process read of a document is kept in deck's base, so a whole text it saves after deck restarted
  is refused over the owner's edit as before the restart; a button drawn before a restart still allows its calls,
  for a week after deck drew it (then: press it again).
  Modules: nothing.
- 2026-11-12: a field of a card puts the phrases its expression writes through the module's `i18n`, as a column of a
  table does. The starter free-models is written in English with its Russian in `i18n`: its scripts write their
  notes in English, its agents in the language deck gives.
  Modules: a phrase the expression of a card field writes gets its translation in `i18n`; a script that writes its
  notes in a language other than English writes them in English, translated in `i18n`.
- 2026-11-12: a run that ended more than 180 days ago keeps only the start of its input and result: `process_get`
  gives each as `{"cut": "<its first 500 characters>"}` once it was longer than 1000; revisions of documents stay
  whole. An incident the master does not take (paused, no `on_incident`, a start that fails) is a note of deck in the
  owner's feed.
  Modules: a script or screen that reads the input or result of a run that ended over 180 days ago takes `{"cut": …}`
  too.
- 2026-11-12, format 17: a module declares in `rights` what it may ask the owner for, each with `why` — the seven
  rights of deck's catalog: `network` (hosts, `"who": "agent"` for the shells of its agents), `login` (`claude-code`,
  `codex`), `data` (another module, `module.collection`, `module/document`, `module:models`, `*:runs`, its own tool
  that reads with a key), `files` (`gpu`, a folder of the project), `money` (a connection, a model, an `@role`),
  `send` (its command or tool, `server.tool`, a command of deck, a site the browser acts on), `modules`. Gone:
  `domains`, `allowed_external`, `reads`, `models`, `gpu`, `class` of commands and HTTP tools, `classes` of MCP
  servers; new: `undo` of a command or tool, `right` of an HTTP tool and of the tools of an MCP server. deck reads the
  right of a tool: GET or HEAD without a key is `network` on its host, with a key `data` on the tool, another method
  `send`. A declaration grants nothing and a save no longer waits for the owner: their answers live apart (always or
  no), enabling grants every declared right but `send`, which asks the first time, and a right declared later the
  owner grants in the module's settings or when it is used; an answer of always holds to the tool or command as it
  was. An agent on a subscription login, or of a module that reads the data of others, has no network in its shell:
  a script installs and clones. A trial of a draft reaches no host, the package registries neither, until the owner
  grants it. What a draft may do is read with `deck.rights_list`, the old read of it gone; the owner answers with `deck.right_set`;
  `manifest_check` and `manifest_save` list `ungranted` instead of `widens`. Stored manifests moved to format 17 and
  keep as always what the owner had granted; an imported module of an older format is raised to it.
  Modules: a prompt or skill that names `domains`, `allowed_external`, `reads`, `models`, `gpu`, `class` or `classes`:
  name the right of `rights` instead; stored manifests moved already, and `manifest_check` refuses the old fields.
- 2026-11-13: one question at first use. Every call deck decides goes by one table, whose row `deck.call_explain`
  names for a call by whom (`in_deck`, `owner`, `cleared`, `refused`, `draft`, `draft_tool`, `undeclared`, `denied`,
  `always`, `for_work`, `pressed`, `in_force`, `ask`). The owner answers a request once, for the work of its process,
  always or no: `ask_answer` takes `allow`, `allow_work`, `allow_always`, `deny` (a no to a right the module declared
  is kept, so it does not ask again; `deny:<reason>` refuses that call alone). A call made without the owner that acts
  in their name, or that deck can take back, is a line among the changes, with the `undo` its command or tool names.
  A tool of an agent's provider the owner confirms is the same request (`kind: call`); a login of a provider is a
  `sign_in`; the kinds `confirm` and `login` are gone. Enabling a module gives in advance `network` of its programs,
  `data` and `files`; `login`, `money`, `send`, `modules` and the network of its agents are asked when first used: a
  turn waits for the answer before it starts (`waiting`, reason `right`), a script's call through `$DECK_MODELS` is
  refused and the owner is asked once. `command_describe` says `needs_right` instead of `class`, the owner's `calls_list` gives
  `need` (the right a call needed) instead of `class`, a button of a custom block says `stays` for a command that
  needs no right. A button a module's agent writes on a note whose command needs a right calls `deck.feed_press`:
  the call of the module, which asks the owner with what it sends. In deck's box Claude Code runs without its auto
  mode, its tools bounded by the box, and Codex asks no approvals; outside a box both run as before.
  Modules: a screen, script or prompt that reads `class` of `command_describe`, or the kinds `confirm` and `login` of
  an ask: read `needs_right`, `call` and `sign_in`.
- 2026-11-13: deck marks what a turn read. An agent's or the master's turn that read text from outside deck — a page
  (`deck.web_fetch`, `deck.web_search`), an answer of an HTTP tool, an MCP server or the browser, data of a module
  that holds such text, a work's result that did, files given with a message, what an automation token wrote — and
  holds something of value (a subscription login, or its module's data) goes out only by the owner's once: `send`,
  `money`, `modules` and `network` on a host it does not name ask each call, whatever the owner answered always (no
  "always" or "for this work" is offered); a host the module names stays as answered. Such a turn also changes the
  prompt, script, skill, custom block or manifest of an enabled module, or a skill of the workspace, only by the owner's
  yes to the diff; a draft it writes freely. A write of such a turn or of a token marks the data of the module it
  writes. The mark lives as long as the session (a new topic, a new run is clean) and a restart keeps it. A child of a
  marked turn starts marked. `deck.web_fetch` needs the right `network` on its host, the master's by its `*` like any
  module's (the owner side's any host is gone). `command_describe` says `brings` (`deck`, `data`, `outside`),
  `process_get` gives `read` and `valued`, `projects_list` gives `untrusted`, `tokens_list` gives `trusted`; the owner
  clears a module with `deck.project_trust` and vouches for a token with `deck.token_trust`. A reader — an agent
  command whose `calls` are only `deck.web_search` and `deck.web_fetch`, in no folder — holds nothing of its module
  (its data folder is its results), so it reads page after page by its right; a call of it is `network` on the hosts
  its module's programs hold. The master's reader is `master.research`; the setting `master_web` (`ask`, `always`)
  lets the master read pages marked by its right of always.
  Modules: a command whose turn reads pages and then sends, spends or edits the module asks the owner each time: split
  the reading off into a reader (`calls` only `deck.web_search` and `deck.web_fetch`, no folder).
- 2026-11-13: an answer that trusts for long takes the owner's passkey touched within five minutes: always to `login`,
  `money`, `send`, `modules`, the network of agents, `network` on `*` and `data` of other modules (not a module's own
  services, `:models`, `:runs`); enabling a draft (`project_enable`), `token_create`, `pair_link`, `project_trust`,
  `token_trust` with `trusted: true` and the setting `master_web` = `always`. Without one the call is refused with
  status 428 and the owner's client asks for a touch; the master, agents and tokens never have one. Once, for this
  work, no and a revoke take none. Enabling or creating a module gives in advance only what takes no passkey:
  `network` of programs on named hosts, `files`, `data` of its own services and `:models`/`:runs`; `network` on `*`
  and the data of other modules the owner grants in Access → Rights. `rights_list` says `key` (the owner's always to
  the row takes a passkey) instead of `at_use`.
  Modules: nothing.
- 2026-11-13: an automation token reaches modules by rights of the catalog: `token_create` takes `commands` (deck's
  alone, `deck.*` every one), `data` (modules whose data deck's reads give it) and `modules` (a module whole: its
  commands and deck's commands acting in it; `module.command`: that command alone); `*` is every module but the
  master, and lets the token write documents of the workspace (skills); others only read them. A token reads nothing without a module named (`project is required`) and changes only modules named whole;
  `tokens_list` gives `data` and `modules`. Tokens of before keep what they named: the commands of a module move to
  `modules`, and one that called deck's commands reaches the modules it named whole, or every module when it named none.
  Modules: nothing; a token of before keeps what it named.
- 2026-11-13: a right `money` takes `run_usd`, the most one run (a process) spends by the owner's always, in US
  dollars by deck's prices: past it an agent's call waits for the owner's yes to that very call (no always or for
  this work is offered) and a script's call is refused (403); a new run starts from nothing; without `run_usd` always
  has no cap. `run_usd` on another right is refused on save. `call_explain` names the row `over`.
  Modules: a `money` right may take `run_usd`, the cap of one run; without it the owner's always has no cap.
- 2026-11-13: the format of manifests stays 17 and is frozen again: a new field of a module comes only with one taken
  out. The skill module-builder is shorter (about 6,400 words, at most 8,000): the rules of each field and block are
  in the `description` of `manifest_format_get`, which the skill no longer repeats; read it first. An edit of the
  skill that adds words takes as many out.
  Modules: nothing; the rules of each field are in the `description` of `manifest_format_get`.
- 2026-11-13: the mark of a turn is of its process, not only of a call it makes now: a button of a note a marked turn
  wrote (`feed_press`) waits for the owner's yes to that very call when it reaches outside, whatever the module holds
  always; the turn's own calls of models through deck are no way out.
  Modules: nothing.
- 2026-11-13: a marked turn (it read text from outside deck and holds something of value) calls a script or tool
  command of a module that holds a way out always (`network` of programs, `send`, `money`, `modules`), or an agent
  command of one holding the network of its agents' shells, only by the owner's yes to that very call: the program
  would carry the turn's words out. A clean turn calls them as before; a press naming the call stays a yes.
  Modules: nothing.
- 2026-11-13: an HTTP tool or MCP server of a module is its own service and never another module: `data` on
  `<tool>` or `<server>.<tool>` reads nothing of a module of that name, and a manifest whose tool or server with
  such a `data` right is named like another module of the workspace is refused on save (rename it).
  Modules: a tool or MCP server with a `data` right named like another module of the workspace: rename it, a save
  refuses it.
- 2026-11-13: what deck puts into a turn carries the mark of where it came from: a turn whose memory, lessons or
  skills of the workspace come from a module marked as holding text from outside starts marked, and so does a
  continuation of a marked process, a process sent back by a marked turn's remark and the master's turn told a note
  that quotes a marked process. A write that names no module — the owner's memory, a skill or document of the
  workspace — marks project master, which `project_trust` clears.
  Modules: nothing.
- 2026-11-14: under `run_usd` a paid call of a model deck cannot price (the API sent no cost, the model has no price in
  deck's settings) counts as past the cap: the next call of that run waits for the owner (an agent) or is refused (a
  script). A model of the owner's machine, a free one and a subscription are not money.
  Modules: a module that calls a model under `run_usd` asks the owner to give that model a price in deck's settings,
  or the run waits for the owner after its first call.
- 2026-11-14: calls of models of one run under `run_usd` go one at a time, each counted with what the ones before it
  cost: a script's parallel calls wait for each other, and the run spends no more than when it calls in a row.
  Modules: nothing.
- 2026-11-14: the owner's no or revoke to `network` or `files` of a module stops its running MCP servers at once; the
  next call of their tools starts them in the box of what the module holds then.
  Modules: nothing.
- 2026-11-14: where text from outside came from (`untrusted` of `projects_list`, `read` of a process and of a chat
  topic) is said in the owner's language; a module's mark reads "the call <command> of <who>" and the data "the data
  of <module>, written by <who>".
  Modules: nothing.
- 2026-11-14: an HTTP tool of POST, PUT, PATCH or DELETE carries a request body its caller writes, whatever right its
  author names (this line holds the one of 2026-11-13 on such tools): a marked turn calls it only by the owner's
  once; one whose `"right"` names `data` sends the body with the owner's key, so enabling the module does not give it,
  the owner's always to it takes the passkey and `rights_list` says it sends the body by its author's word
  (`exit: true`). A GET stays as it was, free on a named host.
  Modules: an HTTP tool that only reads is a GET, free on a named host.
- 2026-11-14: a script of `"runtime": "python"` runs by `uv run --script` on deck's one python (3.11 in the image), so
  its PEP 723 block gives it its packages; uv downloads no python (`UV_PYTHON_DOWNLOADS=never`), and a script whose
  `requires-python` that python does not meet fails as `program.missing` naming both versions. The file of a script
  stays under `work/projects/<id>/scripts/` by its path in the module (a trial's under `drafts/`), so all runs of it
  share one uv environment.
  Modules: a script of `"runtime": "exec"` whose first line is `#!/usr/bin/env -S uv run --script` gets `"runtime":
  "python"` (`manifest_check` names it); a `requires-python` above 3.11 is lowered to it.
- 2026-11-14: the daily cleanup removes a cache of a module untouched for 30 days: each folder of `$DECK_DATA/.cache`
  and of the `.cache` and `.npm` of its MCP servers' homes whole, uv's by the last run of a script of the module, and
  uv's environments of a script beyond one a file. An export of a module carries no caches, by the rule of snapshots
  (folders starting with a dot or named like a cache, `node_modules`, `hf`, `venv`, `site-packages`). The disk row
  names the folder of every module and its caches.
  Modules: a script that keeps what it must not lose in `$DECK_DATA/.cache` keeps it in its data folder.
- 2026-11-14: `deck.project_report` says what a module did in a period (`days`, 1 is today) by the rights of the catalog,
  a line a subject: the hosts its programs and agents reached with how many requests (a tunnel of HTTPS counts once),
  the calls that needed a right (made and `refused`), turns on a login, paid model calls (`usd`) and turns that read
  text from outside deck (right `outside`). The door writes the hosts a box reached to the journal, the owner's
  `calls_list`: lines of outcome `reached` with `subject` (the host) and `count`, at the end of a run and on the way;
  the master reads them summed up by `project_report`. `rights_list` gives
  `used` (the last use), `module` and `title` (the right in the owner's words); an open process gives `opens` (`hosts`
  of its box, `login` of its CLI).
  Modules: nothing.
- 2026-11-14: one screen of rights: Settings → Rights lists the rights of every module under its module or, with
  `params.by` `right`, under the right, each with its answer (`state` of `rights_list`), when last used and a revoke at
  one tap; `params.project` narrows it to one module; a right of the master is deck's own, without buttons. The rights table of a module's settings and the list of rights on the page
  of a draft are gone: the draft's card says how many rights enabling gives and how many ask first, with a button to
  Rights. A module's report is the screen `rights-report` (`project`, `period`). An open process gives `marks` (its
  hosts and login as labels) and a chat topic `opens`; the head of Today says how many runs hold the network or a login.
  Modules: a button of a module that opened the rights of its settings opens the screen `deck/settings` with `params`
  `tab: Rights` and `project`.
- 2026-11-14: `asks_list` gives `waits`, why a request waits for the owner in the owner's words (by the row of the
  table of decisions), and with `by_work` puts the open requests of one process in one item with `parts`, each with its
  own buttons, and buttons for all (verbs `allow_all`, `deny_all`, `allow_work_all`); `ask_answer` takes `also`, other
  requests of the same process answered the same (allow, allow_work or deny). `process_get` gives `why_waits` and
  `waits_ask` of a process whose request is open. New verbs `rights` and `report`; a list item of deck's screens has `parts`.
  Modules: nothing.
- 2026-11-14: weights of models never live in deck: a model is a connection to a ready open-source server or a cloud
  API. `manifest_get`, `manifest_check`, `manifest_save` and an import warn, and still take the module, of `files: gpu`
  with a package that loads weights (torch, diffusers, transformers, …) in a script's PEP 723 header, of mlx, CUDA or
  onnxruntime-gpu in a header, of a script that downloads from Hugging Face or `network` on huggingface.co, and of a
  script of `runtime: node` (a script is Python; node only starts a ready JS engine). `files: gpu` is for rendering
  and video.
  Modules: a script that loads weights or downloads them from Hugging Face calls a model of a connection instead; a
  script of `runtime: node` is rewritten in Python, node only starting a ready JS engine (`manifest_check` names
  both).
- 2026-11-14: the master has the skills `models` (a model is a ready open-source server or a cloud API: a server by
  kind, its install on a Mac or Linux, its connection through a node or a local address, cloud or own by how often,
  a model tried through the cloud) and `first-setup` (three ways to reach models and the assignments of each kind). A
  `files: gpu` right is for rendering and video on Linux; on a Mac the box lets every program use the graphics card.
  The master reads `spend_list` and `spend_days_list`, to count how often a module calls a paid model.
  Modules: nothing.
- 2026-11-14: a usage limit pauses what its answer speaks of: OpenRouter's `free-models-per-*` pauses every free model
  of that connection (health row `limit:<connection>:free`), a limit of a CLI's subscription every way to it, on a node
  too (`limit:<connection>`), any other the model. A limit without a reset is tried again once in two seconds (a CLI,
  which retried already, pauses at once), then pauses 5, 10, 20, 40 minutes and an hour at most. A call of an `@role`
  through `model_call` or `$DECK_MODELS` skips a paused model until its end and calls a model that did not answer
  last for ten minutes; a 429 makes a model paused, not red. The row `limit:<cli>` shows the windows of the
  subscription (5 hours, week: share used, reset); at 98 % new turns go to the next model until the window resets, a
  limit that resets within a minute is waited out on the same login.
  Modules: nothing.
- 2026-11-14: self-checks spend less and show what they spend: a CLI is not checked as deck starts, only on the button,
  after a broken turn and again while red; a model at a limit is checked once, when its pause ends; the models of one
  connection are checked one at a time; the calls of a self-check are in the journal of model calls as command
  `self-check` without a process (`spend_list`), but for a check run on a node. A limit a self-check meets is a pause, not a failed check.
  Modules: nothing.
- 2026-11-15: a built-in CLI (claude-code, codex) deck never checked is checked as deck starts, so a fresh install
  shows it needs a login (without one the CLI refuses before any model call); one checked before still is not.
  Modules: nothing.
- 2026-11-15: search works out of the box: `compose.yaml` runs SearXNG beside deck, and the first run gives a workspace
  with no search the connection `searxng` (`http://searxng:8080`, api raw, local), its model `searxng` (kind search,
  free) and the assignment of search; a search the owner set up stays. deck without Docker sets SearXNG up by the
  master's models skill (the recipe left deck's text of no search).
  Modules: nothing.
- 2026-11-15: a CLI has more than one subscription login: `deck.connection_set {name, api: "cli", agent: "claude-code"
  | "codex"}` adds another login (such as `claude-code-2`), a connection with its own sign-in (`deck.agent_login` by
  its name), homes, self-check, quota and pause, and a model of its name in the CLI (its default model; more by
  `deck.model_set` on that connection), assigned and a fallback as any model; `deck.connection_delete` removes it with
  its sign-in. The right `login` names the login (`login: claude-code-2`): a module on the first login does not run on
  the second until it declares it too. A login runs on deck's machine, not `@<node>`.
  Modules: a module that runs on another login declares `login` on its name too (`login: claude-code-2`).
- 2026-11-17: `deck.screen_get` takes several topics in `topic`, separated by spaces, and reads the blocks any of them
  changes in one read. An answer to an ask that is answered or expired already (`deck.ask_answer`, a question of the
  chat taken back, or one deck closed itself because what it asked about changed) is refused with HTTP 410, not 409:
  what it acted on is over; read it again rather than tell it.
  Modules: a script that tells an ask answered already by 409: read 410.
- 2026-11-18: A call of a turn that brings text from outside (a tool, an MCP server, a browser, a page) marks the
  turn before it runs, whatever it answers: an error of the other side marks it too. A call that is not a read has its
  line in the journal from its start, `making` (an intent to act outside deck makes it `started`); a deck that stopped
  before its outcome leaves the line `unknown`; it asks the owner about an intent to act outside deck and a call the
  owner allowed, as before.
  Modules: nothing.
- 2026-11-18: The files a process that read text from outside deck keeps (by its result or left in its results
  folder) mark the data of its module as holding text from outside, as its writes of records do.
  Modules: nothing.
- 2026-11-19: A call that waited for the owner's yes is made, once allowed, as its caller is then: a script, tool or
  model process as itself (its journal line names it, not an agent), a trial as a trial, a token with the rights it
  has now (a token revoked meanwhile gets the call refused), an agent with the calls its command lists. `call_explain`
  of a process and a button on a note act as the process that wrote it.
  Modules: nothing.
- 2026-11-20: `commands_list` gives the master the form of `deck.project_trust` (Settings → Rights, a dialog that takes
  `project`), so the master links the owner to it; the `runtime` of a script in `manifest_format_get` says that python
  runs by `uv run --script` with its PEP 723 block; `manifest_check`, `manifest_save`, a read of a manifest and an
  import warn of a script of `runtime` `exec` with `uv run` in its shebang. Each line here since 2026-11-10 now ends with
  what a module does about it: read them again from 2026-11-10. The line of 2026-11-13 on HTTP tools of POST, PUT,
  PATCH or DELETE is now within the one of 2026-11-14, and `calls_list` is named the owner's.
  Modules: a script of `"runtime": "exec"` whose first line is `#!/usr/bin/env -S uv run --script` gets
  `"runtime": "python"`.
- 2026-11-20: `manifest_check`, `manifest_save` and an import warn of the lines not in English of a module's scripts,
  its prompt files and the prompts of its commands, a warning a file with the numbers of its lines (a word of two
  letters or more of a script other than Latin, such as Cyrillic); they still take the module. The `i18n` of a
  manifest is not warned.
  Modules: a script that writes its words in another language writes them in English and puts the owner's words in
  `i18n`; a prompt in another language is rewritten in English, its agent writing in the language deck gives.
- 2026-11-20: deck writes the intent of a call of a tool that sends (an HTTP or MCP tool with the right to send, an
  action in deck's browser) itself, before the tool runs; a call the owner makes of a command or tool that sends keeps
  a line of the journal too. The failure of an MCP or browser call whose answer did not come reads
  `deck: <tool>: <why>; the outcome is unknown and the owner decides whether to repeat it`, as an HTTP tool's does,
  and the line of one that was answered or did not leave deck keeps the error its caller got.
  Modules: nothing to change; a call of unknown outcome is still not repeated until the owner decides.
- 2026-11-20: A call over HTTP may carry its key in the header `Deck-Key`: a call again with the key of one deck made,
  as a press of the web client sent again after its answer was lost, gives what the first gave and acts once; a key is
  kept a day, a call still being made or of unknown outcome is refused, and a call that gave a token is not answered
  twice. `deck.chat_send` loses its own retry key: the press of the chat carries its key. A cron trigger's run is
  keyed by its slot, which starts once.
  Modules: nothing to change.
- 2026-11-20: The owner's time zone is a setting (`time_zone`, Settings → Advanced → Limits, an IANA name such as
  Europe/Oslo): cron triggers run by its wall clock and move to it when it is set, and Today, the journal and the
  report of rights count the owner's day in it; deck's own zone (the `TZ` of its process) while it is not set.
  Modules: a cron of a manifest reads the owner's clock as before.
- 2026-11-21: A call says what it did: the answer of `POST /api/commands/<name>` carries the header `Deck-Effect`
  (`done`, `started`, `waiting` for the owner's answer, `unchanged`), and the answer of a tool of deck's MCP server
  `_meta.effect` beside the process it started (`_meta.process`) or the request it waits on (`_meta.ask`); the data
  of the answer is as before. A cron trigger whose call waits for the owner's answer says so in the health of its
  project (`trigger:<project>.<command>`, a warning naming the request) until a run of it starts, where it said
  nothing; a slot fired again after deck stopped names the run it started then.
  Modules: nothing to change; a master that reads the health sees the trigger waiting for the owner.
- 2026-11-21: A change `deck.document_propose` makes by the owner's yes is the owner's edit: its revision's author is
  `owner` and its reason starts with who proposed it ("proposed by master: …"). So when deck ships a new text of the
  same skill or fact, the owner's edit stands where both changed the same place, and you get a note of what of deck's
  new text it lacks to tell the owner.
  Modules: nothing to change.
- 2026-11-21: A turn of an agent whose last call the model API refused (a 400 such as a tool the model named that
  was not given) has no answer, a result schema or not: the work goes on on the next model of its role, else the run
  fails with `model.unavailable` and the words of the API, where a command without a result schema ended done with the
  agent's text of the error.
  Modules: nothing to change; a script that read such a "done" text as a result now sees the run fail.
- 2026-11-21: `deck.web_search` fails with `model.unavailable` naming the engines when a SearXNG answers no results
  because its engines did not answer (`unresponsive_engines`, such as a suspension by a rate limit), where it gave an
  empty list; that search is red in health and the next search of the role takes the query. deck sends at most two
  searches at once and answers the same query of a workspace within 10 minutes by the results it got, its line of the
  journal marked `cached`. SearXNG of compose.yaml asks Bing, Yahoo and Mojeek too.
  Modules: an agent or script that took an empty list as "nothing on the web" now gets the failure; repeating a query
  does not search again within 10 minutes, so change the query instead.
- 2026-11-21: A run of a script asks at its start for the hosts its module declares under network for its programs
  that the owner has not answered, such as hosts added after the module was enabled, where the proxy refused them
  without a word: the run waits (`waiting:right`) on one request that names them all, and "for this work", "always"
  or "no" answers each. The start of an agent asks its login and its agents' hosts in one request too, where it asked
  one at a time. A host the proxy refused a script that ended well is a line `deck: the proxy refused: …` of its log.
  Modules: nothing to change; declare every host a script reaches under network in rights.
- 2026-11-21: deck's MCP server lists its permission tool `approve` only to Claude Code and Codex, which ask it about
  their own tools; an ACP agent such as Goose no longer sees it, where calling it made a request of the owner that
  meant nothing.
  Modules: nothing to change.
- 2026-11-21: A run of a trigger does not wait for hosts of its scripts the owner has not answered: it runs without
  them, its log says `deck: not granted yet: …`, and one open request of the module asks for them, however many runs
  go. `deck.web_search` fails on no results only when none of the web engines of the SearXNG answered (by its
  `/config`); an empty answer beside an engine that missed is an answer. The cache of a query is per the searches of
  the caller's assignment; two requests at once go to each search connection of a workspace, a search takes at most
  30 seconds. A start asks the subscription login in a request of its own, apart from hosts.
  Modules: a script a trigger runs reads `deck: not granted yet` in its log until the owner answers.
- 2026-11-22: The request of a module for hosts of its scripts, which runs of triggers leave, closes by itself once
  the owner answers each of its hosts in the module's rights; a no to it keeps the yes the owner gave one of its hosts
  since. Runs of triggers at once leave one such request.
  Modules: nothing to change.
- 2026-11-22: `deck.web_search` waits at most 10 seconds for a busy search connection, then goes to the next search
  of the role without blaming the busy one; it counts as engines a SearXNG asks the enabled ones of `general` that
  are `web` too, by its `/config` read once in 5 minutes, and a SearXNG that names none answers no results as an answer.
  Modules: nothing to change.
- 2026-11-22: `deck.web_search` takes no results of a SearXNG whose `/config` it cannot read, while engines did not
  answer, as a search that did not answer; no results while engines did not answer are not kept for the same query.
  Modules: nothing to change.
- 2026-11-22: A request of a module for hosts whose every host the owner answered in its rights, one with a no,
  closes refused, not as done; a run of a trigger asks for no host granted or refused a moment before, and requests
  of a right at first use made at once are one.
- 2026-11-21: A change of what a module runs (a prompt, a skill, a script) that a turn which read text from outside
  made by the owner's yes is the owner's: it no longer marks the module or the master, so the turns that read it later
  are not marked by it. An edit of such a turn whose old text does not occur once is refused before anything is
  asked or marked, with the same error as before.
  Modules: nothing to change.
- 2026-11-21: A request of `deck.asks_list` (and `deck.process_asks_list`) heads with the module the call changes:
  `module`, and `headline` — that module and what changes in the owner's words ("tyres: change scripts/search.py"),
  where `project` stays the module that asks. `waits` says where text from outside came from in short (the data of the
  module it last passed through and its first source); `read` holds it whole. The diff of a change of a document is
  `diff`, no more among the lines of `change`, and on Today it is folded under the details.
  Modules: nothing to change; a master that reads requests reads the module they change in `module`.
