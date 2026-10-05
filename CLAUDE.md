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
- ACP agents (OpenCode, `dsh`) on a local Ollama: `DECK_REAL_ACP=1 go test ./tests/scenarios -run TestRealACPAgents -v`
  (`DECK_OLLAMA_MODEL`, default `laguna-xs-2.1:nvfp4`; `DECK_ACP_DSH` — path to `dsh` when it is not in PATH; a cloud
  model: `DECK_ACP_URL`, `DECK_ACP_MODEL`, `DECK_ACP_KEY`)
- Script packages from PyPI and npm through deck's proxy: `DECK_REAL_PACKAGES=1 go test ./tests/scenarios -run TestRealScriptPackages -v`
- Web client in a real browser (Playwright MCP; checks that `web/dist` is fresh):
  `DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v` (`DECK_SCREENSHOTS=<dir>` keeps screenshots)
- Scenario tests on Linux (bubblewrap sandbox, needs docker): `DECK_LINUX=1 go test ./tests/linux -v`
- Regenerate SQL code after editing `internal/store/queries/*.sql` (one file per area) or migrations: `sqlc generate`
- Regenerate the Codex protocol types from the installed CLI: `go generate ./internal/provider/codex`

## Layout

- `cmd/deck` — entry point
- `internal/store` — SQLite: a committed write publishes the topic of its table in the project of the row
  (`changes.go`, the SQLite pre-update hook); the base schema `migrations/00001_base.sql` and goose migrations after it
  (`00002_granted`, `00003_llm_call`, `00004_ask_workspace`: a table rebuilt with foreign keys off is a NO TRANSACTION
  migration; `00005_marks` — `project.imported_from` and the notes for the master as feed rows, `00006_feed_read` —
  warnings as notes of the feed, `00007_sandbox_health`, `00008_alone` — a trial of one command), sqlc queries
  (`db/` is generated); `mark` keeps only cursors and throttles
- `internal/registry` — command registry: every input (API, MCP, triggers) calls commands through it;
  input schemas limited to App Intents types; `registry.Def` takes the schema from the tagged input struct;
  `registry.Read` declares a command that only reads, with its result schema from the Go type and `On` its event
  topics (a read without them panics; `registry.Static` for none), kept by name for the screens (`registry.Reads`);
  `registry.DefOf` declares a command whose parameters are listed in code; `actor.go` (an automation token is its own
  kind `Token`, counted as the master in the callers of a command by `Actor.As`), `errors.go`
- `internal/docs` — documents with revisions, skill files, the manifest reader with its one parse cache (`All` the
  current manifests of projects), `InForce` (a manifest with only the rights of the
  revision the owner granted, `project.granted_revision`), `Patch` (JSON Patch keeping the text around a replace)
- `internal/app` — wiring and the screens: `New` allocates every service first and builds each whole with the
  functions of the others (a test checks none is left unset), registers their commands and runs `Loop`/`Sweep`. Each
  domain is a package `app/<area>` with a `Service` (narrow dependencies as fields, functions of other services as
  fields) and `Commands()`; no domain package imports `internal/app`: `project` (`Service` the lifecycle — create,
  enable, trials, import, delete; `Saver` the manifests — save with checks, migrations and grants, the commands a
  manifest gives the registry), `asks` (what waits for the owner and its answers; `Unheard` hands the master an answer
  its process did not act on), `processes` (the commands of processes), `failures` (failed processes and screens to
  the master), `health` (the list of health), `connections`
  (sign-ins of the browser and oauth2 tools, MCP servers), `documents` (documents, shipped skills, health of
  scripts), `triggers`, `snapshots` (snapshots, cleanup, disk), `journal`, `files`, `folders`, `settings` (one table
  `settingDefs` gives the row, the field of `settings_set` and the edit button), `secrets`, `tokens`, `board`,
  `providers`, `devices` (feed with notes of deck and `feed_read`, push, speech to text), `chat`, `suggest`, `records`
  (records with `where` expressions), `triggers` (also the firing of triggers and `After`), `screens`; who may touch
  what is `app/access`;
  a screen block reads a `registry.Read` command (`"from": "deck.asks_list"`), called as the viewer in
  `app/screen_get.go` (`deck.screen`; the menu `menu.go`, `commands_list.go`, translations `i18n.go`); `app/screens` —
  the sources of screens: records, boards, forms, documents;
  deck's screens are data in `screens.json`: menu Cockpit, Schedule, Settings (tabs Projects, Providers, Secrets,
  Tokens, General, Interface, Health), Journal (Calls, Spend, Files, Snapshots); `project` screens (Documents, the page of
  a document and Settings of the project: its commands and providers, folders, tools, secrets, tokens, health) deck adds
  to every project
- `internal/manifest` — project manifest (format 7: `upgrade.go` raises older ones): `types.go`, `parse.go`,
  `lookup.go`, `collection.go`, rights (`rights.go`: `Widens`, `Clip`, the hosts of agent shells), checks of screens by
  part (`check_*.go`), expressions; one `Resolve` of a value (fixed, `$record.<field>`, `=<expression>` with
  `param` of the screen) and `FieldTypes`; the texts of a manifest and their translation `words.go`
- `internal/gateway` — deck MCP server per process: permission prompts, questions, confirmations, registry
  commands as tools; HTTP entry and domain proxy for scripts (`egress.go`: a name resolves once, the proxy dials its
  addresses, a name reaches only public ones)
- `internal/provider` — model providers; `claudecode` runs the CLI isolated from user settings; `codex` runs
  `codex app-server` on the owner's shared login (`protocol.go` is generated); `acp` runs ACP agents (OpenCode, `dsh`)
  on an OpenAI-compatible model inside deck's sandbox — deck has no tool loop of its own
- `internal/process` — process runner: agent and script executors, start, resume, result schema check, result files;
  agent turns take a slot of the workspace (`slots.go`), the rest wait in `waiting_slot`; what it takes from deck is
  `Deps` (functions), the deadlines of processes are `Due` (ceilings, pauses, silent, empty results)
- `internal/home` — the layout of `DECK_HOME`: `state/` only deck reads, `work/` the folders of runs and projects
- `internal/sandbox` — sandbox of scripts, MCP servers and ACP agents behind one interface: `sandbox-exec` on macOS,
  bubblewrap on Linux; `Close` — deck's folder and the owner's home closed but for the runtimes on PATH (`Runtimes`)
  and the project folders, the old sandbox by the setting `sandbox`; `Watch` — refusals from the log of macOS;
  `Agent` — what an agent CLI may not touch in deck's folder; `Rules` — the setting and who hears a refusal (a note
  of the feed), one dependency of the runner, MCP servers and ACP agents
- `internal/script` — runs project scripts in the sandbox (a script installs its own packages through the proxy)
- `internal/trigger` — cron scheduler with catch-up after sleep
- `internal/health` — health rows: `Set` inside a transaction, `Write` on its own and published; health is the
  state of what deck runs or reaches, events go to the feed
- `internal/screen` — stored screens resolved into A2UI messages without a store of its own (`render.go`, `blocks.go`,
  `data.go`, `items.go` with the reads of a screen kept by source, `actions.go`, `forms.go`, the topics a block follows
  `topics.go`, components and spans from the catalog `catalog.go`, `i18n.go`)
- `internal/events` — the bus: an event is a topic and the project it changed in; a subscriber keeps the events
  waiting for it merged, so none is dropped
- `internal/api` — HTTP API (`GET /api/commands`, `POST /api/commands/{name}`, files) and SSE; screens are the
  `deck.screen` command; `Sandbox` is the second origin of `custom` blocks (proxy page with a CSP without network,
  `/ext-apps.js`), paired with the client's port (`Pair`)
- `internal/tsnet` — deck on the owner's tailnet from inside the binary: HTTPS on 443, the second origin on 8443
- `web` — client: Vite + React + Mantine v9 + `@a2ui/react` (protocol v0.9, `/v0_9` imports), folders by owner in
  `src/`: `app/` the frame of islands (menu `Nav.tsx`, screen, chat panel `ChatPanel.tsx`, `Toolbar.tsx`, `PhoneBar.tsx`,
  the notifications `Bell.tsx`, the icon pattern `Backdrop.tsx`, the address and back button `router.ts`); `screen/`
  a screen (`ScreenView.tsx`, `useScreen.ts`, buttons through the `ScreenActions` context, the catalog `catalog.ts`);
  `blocks/` a file or folder per block registered through `blockImpl.tsx` (frame `Block.tsx`; `table/`, `board/`
  with `@dnd-kit/react`, `form/`, `media/` (pictures, PDF, the player `Player.tsx` with marks), `chart/`,
  `custom/` the MCP Apps host — `@modelcontextprotocol/ext-apps` 1.7.5, 2.x needs zod 4); `chat/` the chat with the master around `chatStore.ts`; `ui/` buttons, confirmation, icons by
  name, markdown (`@a2ui/markdown-it`), values; `hooks/`; `lib/` the API and live events (`events.ts`, `useLive`, one
  `onResume`), `notify.ts`, `format.ts`, `i18n.ts`, the catalog schema; `settings/` look and pushes; `theme/` the look of
  the device (`look.ts`: scheme, accent, density, font, corners — passed to `custom` blocks as host context), accents
  `palette.ts`, themes and surfaces `themes.css`; `locales/`; PWA and Share Target — `public/manifest.webmanifest`,
  `public/sw.js`; `web/dist` is the embedded build — rebuild and commit it with the sources
- `master` — the built-in project `master`, embedded: manifest, prompts, skills (dispatch, topic-summary, interview);
  the chat and memory around it are `app/chat`, suggestions `app/suggest`, the handing of errors to the master is
  `app/failures`, voice and push in `app/devices`; deck's own texts for its agents are `master/templates` (`master.Text`); the client's chat is `web/src/chat/`
- `internal/lint` — code rules checked by `tests/lint` (comments, requirement refs, vertical names)
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
