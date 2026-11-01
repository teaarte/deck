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
