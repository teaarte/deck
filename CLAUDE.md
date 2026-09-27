# deck2

Personal control panel for AI agents: one Go binary, SQLite, web client. Spec: `docs/spec.md` (Russian).
Protocol facts: Claude Code — `docs/spikes/claude-code.md`, Codex app-server — `docs/spikes/codex.md`.
Stage 1 plan: `docs/plan-stage1.md`.

## Commands

- Build: `go build -o deck ./cmd/deck`; web client: `cd web && npm ci && npm run build` (type check + `web/dist`)
- Run: `DECK_HOME=/tmp/deckhome ./deck` (prints the URL with the access token)
- Tests: `go test -race ./...`
- Real Claude Code end-to-end test: `DECK_REAL_CLI=1 go test ./tests/scenarios -run TestRealClaudeCode -v`
- Real Codex end-to-end test (skipped without `codex`): `DECK_REAL_CODEX=1 go test ./tests/scenarios -run TestRealCodex -v`
- Agent turn on a local Ollama: `DECK_REAL_OLLAMA=1 go test ./tests/scenarios -run TestRealOllama -v` (`DECK_OLLAMA_MODEL`, default `laguna-xs-2.1:nvfp4`)
- ACP agents (OpenCode, `dsh`) on a local Ollama: `DECK_REAL_ACP=1 go test ./tests/scenarios -run TestRealACPAgents -v`
  (`DECK_ACP_DSH` — path to `dsh` when it is not in PATH; a cloud model: `DECK_ACP_URL`, `DECK_ACP_MODEL`, `DECK_ACP_KEY`)
- Web client in a real browser (Playwright MCP; checks that `web/dist` is fresh):
  `DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v` (`DECK_SCREENSHOTS=<dir>` keeps screenshots)
- Scenario tests on Linux (bubblewrap sandbox, needs docker): `DECK_LINUX=1 go test ./tests/linux -v`
- Regenerate SQL code after editing `internal/store/queries/*.sql` (one file per area) or migrations: `sqlc generate`
- Regenerate the Codex protocol types from the installed CLI: `go generate ./internal/provider/codex`

## Layout

- `cmd/deck` — entry point
- `internal/store` — SQLite, goose migrations, sqlc queries (`db/` is generated)
- `internal/registry` — command registry: every input (API, MCP, triggers) calls commands through it;
  input schemas limited to App Intents types
- `internal/app` — wiring, builtin `deck.*` commands, projects, documents with revisions, records, cards and board
- `internal/manifest` — project manifest: parsing, validation, expressions
- `internal/gateway` — deck MCP server per process: permission prompts, questions, confirmations, registry
  commands as tools; HTTP entry and domain proxy for scripts
- `internal/provider` — model providers; `claudecode` runs the CLI isolated from user settings; `codex` runs
  `codex app-server` on the owner's shared login (`protocol.go` is generated)
- `internal/process` — process runner: agent and script executors, start, resume, result schema check, result files
- `internal/sandbox` — script sandbox behind one interface: `sandbox-exec` on macOS, bubblewrap on Linux
- `internal/script` — runs project scripts in the sandbox; package environments per project (`uv`, `npm`)
- `internal/trigger` — cron scheduler with catch-up after sleep
- `internal/screen` — stored screens resolved into A2UI messages
- `internal/api` — HTTP API (`GET /api/commands`, `POST /api/commands/{name}`, files) and SSE; screens are the
  `deck.screen` command
- `web` — client: Vite + React + Mantine v9 + `@a2ui/react` (protocol v0.9, `/v0_9` imports); `web/dist` is the
  embedded build — rebuild and commit it with the sources
- `internal/lint` — code rules checked by `tests/lint` (comments, requirement refs, vertical names)
- `schemas` — contracts embedded in the binary: manifest JSON Schema, screen block catalog (`catalog.json`, read by
  the server and the client), App Intents parameter rules
- `tests/scenarios` — scenario tests through the real gateway with a fake provider

## Rules

- Comments: short facts only. No comment blocks longer than 2 lines, no references to requirements
  (FR-/R<n>/NFR-), no reasoning narration. Generated code and `//go:` directives are exempt.
- No names of verticals (vacancies, forecasts, mailboxes, Brier, …) in Go code or the web client outside
  built-in tools and test fixtures. Verticals live in manifests.
- Tests are scenario tests through the registry/API on a real SQLite in a temp dir; no unit tests of internals.
- Every write runs in `Store.Tx`; inside it use only the `ctx` and `q` it passes (`Store.Q` panics there).
  No network calls, git or waiting on processes inside a transaction: the store has one connection, so a
  waiting transaction freezes all of deck. Write the intent, commit, act, then record the outcome in a new one.
  Helpers that may run inside a transaction take `*db.Queries`. Nothing is hardcoded to workspace 1:
  scope comes from the project or the actor.
- New behaviour is a registry command (`deck.<name>` builtin or `<project>.<name>` from a manifest), not a
  new HTTP endpoint.
- Web client (stage 1+): React + Vite + Mantine v9. Mantine v9 is new — check https://mantine.dev before
  writing components; do not use v7/v8 APIs. Known v8→v9 traps (https://mantine.dev/guides/8x-to-9x/):
  `Text`/`Anchor` take `c=` not `color=`; `TypographyStylesProvider` → `Typography`; `Collapse expanded=`;
  `Spoiler defaultExpanded`; `Grid gap=` not `gutter=`; no `positionDependencies`; `@mantine/form` uses
  `schemaResolver` (no `zodResolver`); default radius is `md`; `postcss-preset-mantine` is still required.
- Less of our own code is better: if a model can do it with a script or an agent, do not add it to the core.
