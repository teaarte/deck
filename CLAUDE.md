# deck2

Personal control panel for AI agents: one Go binary, SQLite, web client. Spec: `docs/spec.md` (Russian).
Protocol facts: Claude Code — `docs/spikes/claude-code.md`, Codex app-server — `docs/spikes/codex.md`.

## Commands

- Build: `go build -tags tailnet -o deck ./cmd/deck` (the owner's deck; without the tag there is no tailnet and
  `-tailscale` refuses to start); web client: `cd web && npm ci && npm run build` (type check + `web/dist`)
- Run: `DECK_HOME=/tmp/deckhome ./deck` (prints the URL with the access token; the next port is the second origin of
  `custom` blocks); on the owner's tailnet: `./deck -tailscale deck` (sign-in link in the log and in health)
- Tests: `go test -race -timeout 30m ./...` (one wait, `Until`, two minutes, ×4 under race or `DECK_TEST_SLOW`); the tailnet scenario only with the tag:
  `go test -tags tailnet ./tests/scenarios/owner -run TestPhoneReachesDeckOverTheTailnet`
- Real Claude Code end-to-end test: `DECK_REAL_CLI=1 go test ./tests/scenarios/real -run TestRealClaudeCode -v`
- Real Codex end-to-end test (skipped without `codex`): `DECK_REAL_CODEX=1 go test ./tests/scenarios/real -run TestRealCodex -v`
- The ACP agent Goose on a local Ollama: `DECK_REAL_ACP=1 go test ./tests/scenarios/real -run TestRealACPAgents -v`
  (`DECK_OLLAMA_MODEL`, default `laguna-xs-2.1:nvfp4`; a cloud model: `DECK_ACP_URL`, `DECK_ACP_MODEL`, `DECK_ACP_KEY`)
- Script packages from PyPI and npm through deck's proxy: `DECK_REAL_PACKAGES=1 go test ./tests/scenarios/real -run TestRealScriptPackages -v`
- Web client in a real browser (Playwright MCP; checks that `web/dist` is fresh):
  `DECK_REAL_BROWSER=1 go test ./tests/scenarios/browser -run TestRealBrowser -v` (`DECK_SCREENSHOTS=<dir>` keeps screenshots)
- Scenario tests on Linux (bubblewrap sandbox, a user without privileges as in WSL2, needs docker):
  `DECK_LINUX=1 go test ./tests/linux -v`
- deck's image from `compose.yaml` on a bare docker (docker in docker, Codex on a model of the test):
  `DECK_SERVER=1 go test ./tests/scenarios/real -run TestServerFromScratch -v`
- The desktop launcher (its own module): `cd desktop && go test ./...`; `desktop/build.sh` makes `deck.app` and the
  Windows installer (`brew install makensis`)
- Regenerate SQL code after editing `internal/store/queries/*.sql` (one file per area) or migrations: `sqlc generate`
- Regenerate the Codex protocol types from the installed CLI: `go generate ./internal/provider/codex`

## Layout

A map of the packages: what each owns and the one place to look for a kind of thing. Files are named by what they
hold; look inside a package for the rest.

- `cmd/deck` — entry point and flags.
- `internal/app` — the wiring only: `New` builds every service from the functions of the others (a test checks none
  is left unset), registers their commands, runs `Loop`/`Sweep`; background work goes through `Go(func(ctx)) bool`.
  Each domain is a package `app/<area>` with a `Service` of narrow dependencies and `Commands()`; no domain imports
  `internal/app`. Who may touch what is `app/access`. Deck's screens are data in `app/screens/shipped/` (a file a
  screen).
- `internal/words` — `ru.json`, the one dictionary of deck's words — of screens, of commands and of the sentences
  deck writes, phrases with verbs too (`manifest.TrText`); `words.In(lang)`. One phrase a meaning (`lint.Words`).
- `internal/registry` — every input (API, MCP, triggers) calls commands through it; input schemas are limited to
  App Intents types; `Def` takes the schema from a tagged input struct, `Read` declares a read with its result type
  and event topics (screens read only these; a read without topics panics, `registry.Static` for none).
- `internal/store` — SQLite: one writer (`Tx`; no network or waiting inside), readers apart; a committed write
  publishes the topic of its table (`changes.go`; a table is in a scope or `silent`); goose migrations from the base
  `00043_base.sql`; sqlc queries one file per area (`db/` is generated).
- `internal/events` — the bus: a topic with the project and workspace it changed in; no event is dropped. deck's own
  observers only `Follow` it: beside the commits, merged by workspace with the projects changed.
- `internal/manifest` — the manifest of a module (one format, `version.go`): types, parsing, checks of screens by
  part, expressions (`Where`, `Resolve`), the table of rights (`grants.go`: `Widens` and `Clip` by one table), the
  structural merge of a grant (`merge.go`), texts and their translation.
- `internal/docs` — documents with revisions, skill files, the manifest reader and what is in force (`For`,
  `InForce`); `Ship` lays out what deck ships (master, skills) with a merge of others' edits and removal.
- `internal/process` — the runner of processes: turns of agents, scripts, tools and models, slots, resume, the
  deadlines (`Due`), the hand-on of a refused or silent turn to the next model (`handon.go`, `watch.go`), what a
  process left (`left.go`); what it needs from deck is `Deps`.
- `internal/provider` — model providers behind one interface and one `Outcome` of a turn: `claudecode`, `codex`
  (generated protocol), `acp` (an ACP agent in deck's sandbox on a model through deck's proxy); self-checks.
- `internal/gateway` — deck's MCP server of a process (permissions, questions, confirmations, commands as tools),
  the HTTP entry of scripts, and the proxy of models: `SendHops` is the one loop of fallbacks. `Egress` is the one
  way to the web: a name resolves once, the proxy dials its addresses, a name reaches only public ones; `Fetch` a
  page the same way — new outbound HTTP goes through it.
- `internal/fault` — the one form of a failure: a type of a closed set, whose it is, text and details; a script
  reports its own as `{"error", "type", "details"}` on stdout with a non-zero exit. A refusal of a call at its entry is
  `registry.Error` (its constructors add `deck: `), any other failure `*fault.F`; `fault.Status` is the HTTP status of
  both.
- `internal/theme` — the themes of deck: the schema and catalog (`schemas/theme.json`), the check of tokens, parts,
  limited CSS and contrast, the CSS the client wears; the shipped themes in `shipped/`. `app/themes` its commands.
- `internal/health` — health rows: state of what deck runs or reaches; events go to the feed.
- `internal/ask` — the broker of what waits for the owner; `app/asks` its commands.
- `internal/screen` — stored screens resolved into A2UI messages (blocks, data, actions, forms, topics, i18n): the
  containers of a manifest become the standard `Row`/`Column`/`Tabs`/`Modal`; data goes only by the path of a block;
  `deck.screen_get` with `block` reads one block, the blocks of a closed dialog get no data, a source's `limit` is a page.
- `internal/api` — HTTP API, SSE, files and the second origin of `custom` blocks (`Sandbox`: the guard of
  `sandbox.html` stays its first script — no WebRTC, no frames).
- `internal/sandbox` — the sandbox of scripts, MCP servers and ACP agents (`sandbox-exec` on macOS, bubblewrap on
  Linux), `Door` the one way out (no door, no network), `Env` the one environment.
- `internal/untrusted` — text deck did not write, shaped (`Cut`, `Line`, `Block`) before a prompt, an ask or a
  journal.
- `internal/files` — the files a command input points to and tools answer with (the files service is `app/files`).
- `internal/script`, `internal/trigger`, `internal/tools` (+ `internal/mcphost`), `internal/paths`, `internal/release`,
  `internal/home`, `internal/nonce` — what their names say.
- `internal/tsnet` — deck on the owner's tailnet, only in a build with the tag `tailnet`.
- `internal/lint` — code rules checked by `tests/lint`; the rules of Go (`rules.go`) keep their current exceptions by
  file:line in `exceptions.go`, a list that only shrinks.
- `desktop` — the launcher on the desktop (Wails, a Go module of its own): tray, window on deck's address, start at
  login; on Windows deck runs in its WSL2 distribution `deck` (`docs/spikes/windows.md`). deck knows nothing of it.
- `starters` — the modules deck offers on its first run, a folder each as `deck.project_export` makes it; data, no
  code of deck for any of them, roles rather than the connections of their author.
- `master` — the built-in project master (manifest, prompts, skills) and `templates`, deck's texts for agents: the
  rule every agent follows (`agent.md`), the prompt of a command, the words of a chat turn.
- `skills` — the skills deck ships to agents; `schemas` — manifest schema, screen block catalog, App Intents rules;
  `deck_only` marks what only deck's own screens use (the format a model reads drops it, a module with it warns).
- `web` — the client: Vite + React + Mantine v9 + `@a2ui/react` v0.9 — import from `@a2ui/react/v0_9` and
  `@a2ui/web_core/v0_9` (the root export is v0.8); folders by owner in `src/` (`app` the frame,
  `screen`, `cockpit`, `apps` the cards that stand for the side menu in that look, `blocks` a folder or file per block,
  `chat`, `ui`, `hooks`, `lib`, `settings`, `theme`);
  `custom` blocks are hosted with `@modelcontextprotocol/ext-apps` 2; `web/dist` is the embedded build — rebuild and
  commit it with the sources. Every button presses through `screen/press.ts`: an A2UI action of its block's context,
  the one handler in `useScreen` acts (`screen/act.ts`, `outcome()`) and replies in `/press/<reply>`; a block never
  calls a command itself, and a button the client needs comes from the server in the block's data.
  `web/foreign` is a test page: a screen in the stock renderer with deck's catalog alone.
- `tests/harness` — the one harness of the tests: `New` with options and `Restart`, the owner's client
  (`Call`/`Must`/`Refused`, `In`, `As`), the one wait `Until` (and `WaitState`, `WaitAsk`, `WaitEvent`), `Manifest` on
  the current format, `Needs` for tests of real programs, the fakes of agents, CLIs and MCP servers (`fakes`).
- `tests/scenarios/<area>` — scenario tests through the real gateway with a fake provider, a package an area
  (`box`, `browser`, `chat`, `egress`, `modules`, `owner`, `providers`, `real`, `runs`), a file `<area>_<topic>_test.go`;
  `tests/lint` keeps their conventions (`internal/lint/tests.go`).
- `.github` — `ci.yml` on every push and PR (checks, tests, race shards a package of scenarios each by `shard.sh` on Linux and macOS, `tests/linux`),
  `image.yml` (the image on its changes and nightly), `release.yml` (a tag only of a green commit of master);
  `renovate.json` keeps the pinned base images, CLIs, modules and actions current.

## Rules

- Comments: short facts only. No comment blocks longer than 2 lines, no references to requirements
  (FR-/R<n>/NFR-), no reasoning narration. Generated code and `//go:` directives are exempt.
- No names of verticals (vacancies, forecasts, mailboxes, Brier, …) in code (Go, the web client, SQL, Python), in
  `schemas/` or `master/`, outside built-in tools and test fixtures. Verticals live in manifests.
- Tests are scenario tests through the registry/API on a real SQLite in a temp dir; no unit tests of internals The one
  exception: contract tests of a provider adapter on recorded streams (`tests/scenarios/providers/contract_<provider>_test.go`).
- Every write runs in `Store.Tx`; inside it use only the `ctx` and `q` it passes (`Store.Q` panics there).
  No network calls, git or waiting on processes inside a transaction: the store has one connection, so a
  waiting transaction freezes all of deck. Write the intent, commit, act, then record the outcome in a new one.
  Helpers that may run inside a transaction take `*db.Queries`. Nothing is hardcoded to workspace 1:
  scope comes from the project or the actor.
- A new restriction of the manifest is a warning on read and an error only on save: `manifest.Parse` runs on every
  read, so a plain new check turns off saved modules. A new format comes with a migration that rewrites every stored
  manifest to it; deck reads only the current one.
- New behaviour is a registry command (`deck.<name>` builtin or `<project>.<name>` from a manifest), not a
  new HTTP endpoint.
- Web client (stage 1+): React + Vite + Mantine v9. Mantine v9 is new — check https://mantine.dev before
  writing components; do not use v7/v8 APIs. Known v8→v9 traps (https://mantine.dev/guides/8x-to-9x/):
  `Text`/`Anchor` take `c=` not `color=`; `TypographyStylesProvider` → `Typography`; `Collapse expanded=`;
  `Spoiler defaultExpanded`; `Grid gap=` not `gutter=`; no `positionDependencies`; `@mantine/form` uses
  `schemaResolver` (no `zodResolver`); default radius is `md`; `postcss-preset-mantine` is still required.
- Less of our own code is better: if a model can do it with a script or an agent, do not add it to the core.
