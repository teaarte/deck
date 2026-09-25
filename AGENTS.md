# deck2

Personal control panel for AI agents: one Go binary, SQLite, web client. Spec: `docs/spec.md` (Russian).
Protocol facts about Claude Code: `docs/spikes/claude-code.md`.

## Commands

- Build: `go build -o deck ./cmd/deck`
- Run: `DECK_HOME=/tmp/deckhome ./deck` (prints the URL with the access token)
- Tests: `go test -race ./...`
- Real Claude Code end-to-end test: `DECK_REAL_CLI=1 go test ./tests/scenarios -run TestRealClaudeCode -v`
- Regenerate SQL code after editing `internal/store/queries.sql` or migrations: `sqlc generate`

## Layout

- `cmd/deck` — entry point
- `internal/store` — SQLite, goose migrations, sqlc queries (`db/` is generated)
- `internal/manifest` — project manifest: parsing, validation, expressions
- `internal/gateway` — deck MCP server per process: permission prompts, questions, confirmations, records
- `internal/provider` — model providers; `claudecode` runs the CLI isolated from user settings
- `internal/process` — process runner: start, resume, result schema check
- `internal/trigger` — cron scheduler with catch-up after sleep
- `internal/screen` — stored screens resolved into A2UI messages
- `internal/api` — HTTP API and SSE; `web/static` — embedded client
- `tests/scenarios` — scenario tests through the real gateway with a fake provider

## Rules

- Comments: short facts only. No comment blocks longer than 2 lines, no references to requirements
  (FR-/R<n>/NFR-), no reasoning narration. Generated code and `//go:` directives are exempt.
- No names of verticals (vacancies, forecasts, mailboxes, Brier, …) in Go code or the web client outside
  built-in tools and test fixtures. Verticals live in manifests.
- Tests are scenario tests through the registry/API on a real SQLite in a temp dir; no unit tests of internals.
- Web client (stage 1+): React + Vite + Mantine v9. Mantine v9 is new — check https://mantine.dev before
  writing components; do not use v7/v8 APIs.
- Less of our own code is better: if a model can do it with a script or an agent, do not add it to the core.
