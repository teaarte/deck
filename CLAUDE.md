# deck2

Personal control panel for AI agents: one Go binary, SQLite, web client. Spec: `docs/spec.md` (Russian).
Protocol facts about Claude Code: `docs/spikes/claude-code.md`. Stage 1 plan: `docs/plan-stage1.md`.

## Commands

- Build: `go build -o deck ./cmd/deck`
- Run: `DECK_HOME=/tmp/deckhome ./deck` (prints the URL with the access token)
- Tests: `go test -race ./...`
- Real Claude Code end-to-end test: `DECK_REAL_CLI=1 go test ./tests/scenarios -run TestRealClaudeCode -v`
- Regenerate SQL code after editing `internal/store/queries.sql` or migrations: `sqlc generate`

## Layout

- `cmd/deck` — entry point
- `internal/store` — SQLite, goose migrations, sqlc queries (`db/` is generated)
- `internal/registry` — command registry: every input (API, MCP, triggers) calls commands through it;
  input schemas limited to App Intents types
- `internal/app` — wiring, builtin `deck.*` commands, projects, documents with revisions, records
- `internal/manifest` — project manifest: parsing, validation, expressions
- `internal/gateway` — deck MCP server per process: permission prompts, questions, confirmations, registry
  commands as tools
- `internal/provider` — model providers; `claudecode` runs the CLI isolated from user settings
- `internal/process` — process runner: start, resume, result schema check
- `internal/trigger` — cron scheduler with catch-up after sleep
- `internal/screen` — stored screens resolved into A2UI messages
- `internal/api` — HTTP API (`GET /api/commands`, `POST /api/commands/{name}`, screens) and SSE;
  `web/static` — embedded client
- `internal/lint` — code rules checked by `tests/lint` (comments, requirement refs, vertical names)
- `schemas` — contracts embedded in the binary: manifest JSON Schema, App Intents parameter rules
- `tests/scenarios` — scenario tests through the real gateway with a fake provider

## Rules

- Comments: short facts only. No comment blocks longer than 2 lines, no references to requirements
  (FR-/R<n>/NFR-), no reasoning narration. Generated code and `//go:` directives are exempt.
- No names of verticals (vacancies, forecasts, mailboxes, Brier, …) in Go code or the web client outside
  built-in tools and test fixtures. Verticals live in manifests.
- Tests are scenario tests through the registry/API on a real SQLite in a temp dir; no unit tests of internals.
- Every write runs in `Store.Tx`; inside it use only the `ctx` and `q` it passes (`Store.Q` panics there).
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
