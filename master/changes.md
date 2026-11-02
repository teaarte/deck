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
