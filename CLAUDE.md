# deck2

Personal control panel for AI agents: one Go binary, SQLite, web client. Spec: `docs/spec.md` (Russian).
Protocol facts: Claude Code — `docs/spikes/claude-code.md`, Codex app-server — `docs/spikes/codex.md`.
Stage 1 plan: `docs/plan-stage1.md`; next: `docs/plan-models.md` (audit fixes, cleanup, models and connections).

## Commands

- Build: `go build -o deck ./cmd/deck`; web client: `cd web && npm ci && npm run build` (type check + `web/dist`)
- Run: `DECK_HOME=/tmp/deckhome ./deck` (prints the URL with the access token; the next port is the second origin of
  `custom` blocks); on the owner's tailnet: `./deck -tailscale deck` (sign-in link in the log and in health)
- Tests: `go test -race -timeout 30m ./...`
- Real Claude Code end-to-end test: `DECK_REAL_CLI=1 go test ./tests/scenarios -run TestRealClaudeCode -v`
- Real Codex end-to-end test (skipped without `codex`): `DECK_REAL_CODEX=1 go test ./tests/scenarios -run TestRealCodex -v`
- The ACP agent Goose on a local Ollama: `DECK_REAL_ACP=1 go test ./tests/scenarios -run TestRealACPAgents -v`
  (`DECK_OLLAMA_MODEL`, default `laguna-xs-2.1:nvfp4`; a cloud model: `DECK_ACP_URL`, `DECK_ACP_MODEL`, `DECK_ACP_KEY`)
- Script packages from PyPI and npm through deck's proxy: `DECK_REAL_PACKAGES=1 go test ./tests/scenarios -run TestRealScriptPackages -v`
- Web client in a real browser (Playwright MCP; checks that `web/dist` is fresh):
  `DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v` (`DECK_SCREENSHOTS=<dir>` keeps screenshots)
- Scenario tests on Linux (bubblewrap sandbox, needs docker): `DECK_LINUX=1 go test ./tests/linux -v`
- Regenerate SQL code after editing `internal/store/queries/*.sql` (one file per area) or migrations: `sqlc generate`
- Regenerate the Codex protocol types from the installed CLI: `go generate ./internal/provider/codex`

## Layout

- `cmd/deck` — entry point
- `internal/store` — SQLite: a committed write publishes the topic of its table in the project of the row
  (`changes.go`, the SQLite pre-update hook; the scope of a table is a closed set, a table no screen reads is in
  `silent`, and a table in neither keeps deck from starting; an update that changes no value publishes nothing); the base schema `migrations/00001_base.sql` and goose migrations after it
  (`00002_granted`, `00003_llm_call`, `00004_ask_workspace`: a table rebuilt with foreign keys off is a NO TRANSACTION
  migration; `00005_marks` — `project.imported_from` and the notes for the master as feed rows, `00006_feed_read` —
  warnings as notes of the feed, `00007_sandbox_health`, `00008_alone` — a trial of one command, `00009_models` — connections, models and
  assignments from the providers of settings, `00010_spend` — the connection of a model call,
  `00011_whisper` — the whisper.cpp of the machine as the model of speech to text, `00012_no_project_limit`,
  `00013_google_openai` — a Google connection on its OpenAI-compatible address, `00014_pause` — a pause without an end
  of its own, `00015_no_opencode`, `00016_orphan_health`, `00017_no_run_later`, `00018_no_suggest`,
  `00019_no_board` — cards, lane triggers and the card column of documents and topics go, `00020_no_limit` — no daily
  limit of a connection, `00021_draft_triggers` — why a schedule of a draft did not start goes once it is enabled, `00022_health_seen` — a
  failing check the owner read stops counting until it changes, `00023_unit` — what a price of a model counts, `00024_outbox` —
  what deck does after a commit: SQL triggers on a deleted file and a closed process write it, the files service and
  the runner drain it after the commit, on every tick and at start, `00025_document_lookup`, `00026_record_mark` — a
  count of the writes of the records of a project, by SQL triggers, `00027_workspace_health` — the health of a model of
  a connection (`model:`) and of pushes is of its workspace, `00033_reopened` — when a process was last sent back to
  work, the base of its duration and of its week in the cockpit, `00034_folder_outbox` — the folder of a deleted record
  and, by an SQL trigger, of a deleted project go through the outbox, `00035_chat_file_ids` — the files of a
  chat message are stored file ids, `00036_retention_subject` — the health of retention is `retention:<project>`, `00037_file_process` — the process that made a file, `00038_no_dsh`, `00039_topic_model` — the model a chat topic answers on, `00040_fault` — the type and details of a failure of a process, the failed process one follows, the same unread note of deck counted), `00041_again` — the input of one more run of a script, tool or model command a call for its record left while it ran, deck's messages to a process, the stops deck makes `deck.stopped`, sqlc queries (`db/` is generated; a parameter compared with 0 or '' is CAST, the linter
  refuses `interface{}` there); `Store.Q` reads on connections of its own beside the one writer, `Tx` waits for the
  writer at most `Store.Wait`, `Backup` copies the base; `mark` keeps only cursors and throttles
- `internal/registry` — command registry: every input (API, MCP, triggers) calls commands through it;
  input schemas limited to App Intents types; `registry.Def` takes the schema from the tagged input struct;
  `registry.Read` declares a command that only reads, with its result schema from the Go type and `On` its event
  topics (a read without them panics; `registry.Static` for none), kept by name for the screens (`registry.Reads`,
  declared at start by `registry.Declare` from `app`'s `init`, before any manifest parses);
  `actor.go` (an automation token is its own kind `Token`, counted as the master in the callers of a command by
  `Actor.As`), `errors.go`
- `internal/docs` — documents with revisions, skill files, the manifest reader with its one parse cache (`All` the
  current manifests of projects), `For` (the one current manifest in force for whoever reads rights: what the owner
  granted, `project.granted_revision`; a trial of a draft also its reads and models, `manifest.Trial`), `InForce` (a
  given revision with only the granted rights), `Editable`, `Edit` (old parts of a text replaced with new ones)
- `internal/app` — wiring and the screens: `New` allocates every service first and builds each whole with the
  functions of the others (a test checks none is left unset), registers their commands and runs `Loop`/`Sweep`;
  background work goes through `background.Go`, which refuses once `Stop` began. Each
  domain is a package `app/<area>` with a `Service` (narrow dependencies as fields, functions of other services as
  fields) and `Commands()`; no domain package imports `internal/app`: `project` (`Service` the lifecycle — create,
  enable, trials, import, delete; `Saver` the manifests — save with checks, migrations and grants, the commands a
  manifest gives the registry), `asks` (what waits for the owner and its answers; `Unheard` hands the master an answer
  its process did not act on), `processes` (the commands of processes), `failures` (failed processes and screens to
  the master), `health` (the list of health), `signins`
  (sign-ins of the browser and oauth2 tools, MCP servers), `documents` (documents, shipped skills, health of
  scripts), `snapshots` (snapshots, cleanup, disk), `journal`, `files`, `folders`, `settings` (one table
  `settingDefs` gives the row, the names `settings_set` takes and the edit button), `secrets`, `tokens`,
  `providers` (connections, models with prices and options, assignments with fallbacks — kinds text, image, tts,
  stt, music, video (deck polls the job, `video.go`), search (a SearXNG the agents in ACP agents search through, `web.go` with `web_search` and `web_fetch`) —, a module calls only models of deck's settings (`Route`) and proposes one by `model_set`, held for the
  owner; a model that did not answer is red in health with the model that took its work; the providers of agent
  turns — built-in or a model in an ACP agent, the agents found with their version, self-checks at start, by button
  and on a broken turn — the routes, limits and rights of direct calls `deck.model_call` (a script's call through the proxy goes by its rules) and of
  the model of an ACP turn), `devices` (feed with notes of deck and `feed_read`, push, speech to text through
  `deck.model_call`), `chat`, `records`
  (records with `where` expressions), `triggers` (also the firing of triggers and `After`), `screens`; who may touch
  what is `app/access`;
  a screen block reads a `registry.Read` command (`"from": "deck.asks_list"`), called as the viewer in
  `app/screen_get.go` (`deck.screen`; the menu `menu.go`, `commands_list.go`, translations `i18n.go`), a collection
  through `deck.records_list`, of another project with the rights of the project of the screen (`Scope.By`);
  `app/screens` — the sources of screens: forms, documents; files of a domain are named by what they hold;
  deck's screens are data, a file each in `app/shipped/` (`project-<name>.json` for those of every project, `menu.json`,
  `i18n.ru.json`, the phrases of read commands `words.ru.json`): Cockpit (the link at the head of the menu), and in the menu Schedule
  (tabs Schedule — what runs by itself with a picker of its model —, Runs), Spend (today, 7 and 30 days by project and
  model, a subscription apart from the money), Files (tabs Files, Data folders, Documents of the master — tiles of one file manager, the media block — and Snapshots), Settings (tabs
  Projects as cards, Models — of every command, of scripts, everywhere else, the models —, Connections with secrets and
  tokens, Interface, Health with the agents and the limits; a module is exported to a .zip and imported from one, `project_export`), the
  page of a draft (`draft.json`: what it may do once enabled, and Enable), the page of a run shows its calls of the
  gateway; `project` screens (Documents, the page of
  a document and Settings of the project in tabs Project (its commands with their schedule and a picker of their model, the models
  of its scripts, its model calls), Access (folders, secrets, tools, tokens), Health, Files) deck adds
  to every project
- `internal/manifest` — project manifest (format 12: `upgrade.go` raises older ones): `types.go`, `parse.go`,
  `lookup.go`, `collection.go`, `nav.go` (`Nav` — a project in the menu, `Tr`), rights (`rights.go`: `Widens` and `Clip` over the same rights — what is in force widens nothing beyond the grant —, the hosts of agent shells), checks of screens by
  part (`check_*.go`), expressions; one `Resolve` of a value (fixed or `=<expression>` with
  `param` of the screen) and `FieldTypes`; the texts of a manifest and their translation `words.go`
- `internal/gateway` — deck MCP server per process: permission prompts, questions, confirmations, registry
  commands as tools (a long call sends MCP progress to a client that asked) (`renew` gives live sessions the commands of a changed project); HTTP entry of scripts (`ScriptHandler`, deck's API
  only) and `Egress` — the web of a door (`egress.go`: a name resolves once, the proxy dials its addresses, a name
  reaches only public ones; `Fetch` a page the same way); the proxy of models (`model_proxy.go`: `DECK_MODELS` of scripts, routed once and the hops
  passed to the rules, `WithRoute`; `model_ask.go`: `AskModel` — the one model of deck's settings a request names,
  `X-Deck-Model` only for a raw service, `WithModel` — the body goes with the model deck checked; `model_hops.go`:
  `SendHops` — the one loop of fallbacks of scripts, commands and the model of ACP turns, with deck's key;
  `model_meter.go`: the usage of OpenAI, Google and Anthropic answers)
- `internal/provider` — model providers; `claudecode` runs the CLI isolated from user settings; `codex` runs
  `codex app-server` on the owner's shared login (`protocol.go` is generated); `acp` runs the ACP agent Goose
  on an OpenAI-compatible model inside deck's sandbox, the model through deck's proxy — deck has no tool loop of its own;
  `Stateful` keeps clients of deck's MCP servers on the handshake (no `server/discover`, go-sdk #1111); `Within`/`Under`
  — the one check that a path is inside a folder
- `internal/fault` — the one form of a failure of a process: a type of a closed set (`model.limit`, `script.failed`,
  …), whose it is (model, deck, module, environment, owner), the text for the owner and the details apart; a script
  reports its own as `{"error", "type", "details"}` on stdout with a non-zero exit
- `internal/process` — process runner: agent and script executors, start, resume, result schema check, result files;
  agent turns take a slot of the workspace (`slots.go`), the rest wait in `waiting_slot`; a call for a record
  whose process of the command runs joins it — a message to an agent, one more run after a script (`join.go`); an
  agent of a record reads the last processes of it, a hand-on what the refused one did and heard (`thread.go`); a tool or model command is
  one gateway call (`tool.go`); what it takes from deck is
  `Deps` (functions), the deadlines of processes are `Due` (ceilings, pauses, silent, empty results); a turn its model refused (a limit, no answer) goes on as a
  new process on the next model of the assignment, told what the first did (`handon.go`), else a limit pauses
- `internal/home` — the layout of `DECK_HOME`: `state/` only deck reads (deck's key of pushes in `state/push`, apart
  from the secrets), `work/` the folders of runs and projects
- `internal/sandbox` — sandbox of scripts, MCP servers and ACP agents behind one interface: `sandbox-exec` on macOS,
  bubblewrap on Linux; `Door` (`door.go`) — the one way out of a confined program: a listener in a short folder of
  its own (Linux; TCP on loopback on macOS), a proxy with a key of its own to the program's domains (`Web`, deck's `gateway.Egress`), deck's handler at
  its address, `Env` put last, `Close` ending its tunnels; no door, no network; `Env` — the one environment of scripts,
  ACP agents and MCP servers (kept variables, own home); `Close` — deck's folder and the owner's home closed but for the runtimes on PATH (`Runtimes`)
  and the project folders; `Agent` — what an agent CLI may not touch in deck's folder; `OpenHint` — where the owner
  opens a folder, at the end of the error of a failed script
- `internal/script` — runs project scripts in the sandbox (a script installs its own packages through the proxy);
  `environ` is `sandbox.Env` with the folders of a script and its door, `Check` the programs its manifest names
- `internal/trigger` — cron scheduler with catch-up after sleep
- `internal/health` — health rows: `Set` inside a transaction, `Write` on its own and published, the statuses and
  their order (`Worse`); a row is of the machine (`provider:`, written through `process.ProviderHealth` and lifted by `MarkHealthy`), of a
  workspace (`model:`, `push`) or of a project; health is the state of what deck runs or reaches, events go to the feed
- `internal/screen` — stored screens resolved into A2UI messages without a store of its own (`render.go`, `blocks.go`,
  `data.go`, `items.go` with the reads of a screen kept by source, `actions.go`, `forms.go`, the topics a block follows
  `topics.go`, components and spans from the catalog `catalog.go`, `i18n.go`); a document block reads
  `deck.document_get` with its history as the viewer, once
- `internal/events` — the bus: an event is a topic and the project it changed in (`Emit`; folders, assignments and
  MCP tools have topics of their own); a subscriber keeps the events
  waiting for it merged, so none is dropped
- `internal/api` — HTTP API (`POST /api/commands/{name}`, files) and SSE; screens are the
  `deck.screen` command; `Sandbox` is the second origin of `custom` blocks (proxy page with a CSP without network;
  the block runs in its frame of an opaque origin with the guard of `sandbox.html` as its first script — no WebRTC,
  no frames, which would bring it back —, `/ext-apps.js`), paired with the client's port (`Pair`)
- `internal/tsnet` — deck on the owner's tailnet from inside the binary: HTTPS on 443, the second origin on 8443
- `web` — client: Vite + React + Mantine v9 + `@a2ui/react` (protocol v0.9, `/v0_9` imports), folders by owner in
  `src/`: `app/` the frame of islands (menu `Nav.tsx`, screen, chat panel `ChatPanel.tsx`, `Toolbar.tsx`, `PhoneBar.tsx`,
  the notifications `Bell.tsx`, the icon pattern `Backdrop.tsx`, the address and back button `router.ts`: tab and
  dialog only the client reads); `screen/`
  a screen (`ScreenView.tsx`, `useScreen.ts`, buttons through the `ScreenActions` context, the catalog `catalog.ts`; the
  top of the cockpit `Hero.tsx` and its one-by-one `Triage.tsx` over the first list of decisions, `decisions.ts`);
  `blocks/` a file or folder per block registered through `blockImpl.tsx` (frame `Block.tsx`; `table/`, `form/`, `media/` (pictures, PDF, audio and video in the browser's player, the lightbox), `chart/`,
  `custom/` the MCP Apps host — `@modelcontextprotocol/ext-apps` 1.7.5, 2.x needs zod 4); `chat/` the chat with the master around `chatStore.ts`; `ui/` the mascot `deki/Deki.tsx` (poses in `public/deki`, a switch of Interface), buttons in `actions/` (a failed press shows its error
  at the button, `Failed.tsx`; the undo toast `undo.tsx`; confirmation), icons by
  name, markdown (`@a2ui/markdown-it`), values; `hooks/`; `lib/` the API and live events (`events.ts`, `useLive`, one
  `onResume`), `notify.ts`, `format.ts`, `i18n.ts`, the catalog schema; `settings/` look and pushes; `theme/` the look of
  the device (`look.ts`: scheme, accent, density, font, corners — passed to `custom` blocks as host context), accents
  `palette.ts`, themes and surfaces `themes.css`; `locales/`; PWA and Share Target — `public/manifest.webmanifest`,
  `public/sw.js`; `web/dist` is the embedded build — rebuild and commit it with the sources
- `master` — the built-in project `master`, embedded: manifest, prompts, skills (dispatch, topic-summary); the daily `review`
  and `on_error` tell the owner with a note of the feed;
  the chat and memory around it are `app/chat`, the handing of errors to the master is
  `app/failures`, voice and push in `app/devices`; deck's own texts for its agents are `master/templates` (`master.Text`); the client's chat is `web/src/chat/`
- `internal/ask` — the broker of what waits for the owner (asks: questions, confirmations, sign-ins, frozen calls) and
  their answers; `app/asks` is its service of commands
- `internal/files` — the files a command input points to and the files tools answer with (not the files service,
  which is `app/files`)
- `internal/tools` — the built-in tools: HTTP tools of manifests, OAuth 2, the browser, MCP servers as commands;
  `internal/mcphost` starts those servers on demand and again after a crash
- `internal/untrusted` — text deck did not write, shaped before a prompt, an ask or a journal (`Cut`, `Line`,
  `Block`); `internal/nonce` — random tokens
- `internal/lint` — code rules checked by `tests/lint` (comments, requirement refs, vertical names, untyped sqlc
  parameters)
- `schemas` — contracts embedded in the binary: manifest JSON Schema, screen block catalog (`catalog.json`, read by
  the server and the client), App Intents parameter rules
- `tests/scenarios` — scenario tests through the real gateway with a fake provider; helpers in `*_harness_test.go`,
  one file per thing they drive (env, provider, process, screen, clock, script, cli, …)

## Rules

- Comments: short facts only. No comment blocks longer than 2 lines, no references to requirements
  (FR-/R<n>/NFR-), no reasoning narration. Generated code and `//go:` directives are exempt.
- No names of verticals (vacancies, forecasts, mailboxes, Brier, …) in Go code or the web client outside
  built-in tools and test fixtures. Verticals live in manifests.
- Tests are scenario tests through the registry/API on a real SQLite in a temp dir; no unit tests of internals The one
  exception: contract tests of a provider adapter on recorded streams (`tests/scenarios/contract_<provider>_test.go`).
- Every write runs in `Store.Tx`; inside it use only the `ctx` and `q` it passes (`Store.Q` panics there).
  No network calls, git or waiting on processes inside a transaction: the store has one connection, so a
  waiting transaction freezes all of deck. Write the intent, commit, act, then record the outcome in a new one.
  Helpers that may run inside a transaction take `*db.Queries`. Nothing is hardcoded to workspace 1:
  scope comes from the project or the actor.
- A new restriction of the manifest comes with a format bump (a step in `manifest.Upgrade`), or is a warning on
  read and an error only on save: `manifest.Parse` runs on every read, so a plain new check turns off saved modules.
- New behaviour is a registry command (`deck.<name>` builtin or `<project>.<name>` from a manifest), not a
  new HTTP endpoint.
- Web client (stage 1+): React + Vite + Mantine v9. Mantine v9 is new — check https://mantine.dev before
  writing components; do not use v7/v8 APIs. Known v8→v9 traps (https://mantine.dev/guides/8x-to-9x/):
  `Text`/`Anchor` take `c=` not `color=`; `TypographyStylesProvider` → `Typography`; `Collapse expanded=`;
  `Spoiler defaultExpanded`; `Grid gap=` not `gutter=`; no `positionDependencies`; `@mantine/form` uses
  `schemaResolver` (no `zodResolver`); default radius is `md`; `postcss-preset-mantine` is still required.
- Less of our own code is better: if a model can do it with a script or an agent, do not add it to the core.
