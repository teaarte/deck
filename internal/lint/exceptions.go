package lint

// exceptions are the places that break a rule today, by file:line; the lists only shrink.
var exceptions = map[string][]string{
	"now": {
		"internal/app/asks/process_asks.go:80", "internal/app/asks/process_asks.go:84",
		"internal/app/asks/process_asks.go:86", "internal/app/config.go:58", "internal/app/files/thumbs.go:31",
		"internal/app/files/thumbs.go:32", "internal/app/processes/processes.go:291",
		"internal/app/processes/processes.go:294", "internal/app/processes/processes.go:298",
		"internal/models/hops.go:149", "internal/models/hops.go:159",
		"internal/models/meter.go:23", "internal/models/meter.go:32",
		"internal/provider/acp/acp.go:183",
		"internal/tools/oauth.go:79", "internal/tools/oauth.go:90",
	},
	"exec": {
		"internal/app/updates/updates.go:18", "internal/provider/codex/gen/main.go:11",
		"internal/script/run.go:14", "internal/script/runtime.go:7",
	},
	"write": {
		"cmd/deck-release/main.go:53", "cmd/deck-release/main.go:56", "cmd/deck/main.go:57", "cmd/deck/main.go:253",
		"internal/app/snapshots/snapshot.go:101", "internal/app/updates/updates.go:286", "internal/process/settle.go:123",
		"internal/provider/codex/gen/main.go:93", "internal/provider/codex/gen/main.go:104",
		"internal/provider/shellcheck.go:79", "internal/provider/shellcheck.go:80", "internal/script/run.go:70",
	},
	"env": {
		"cmd/deck-release/main.go:33", "internal/provider/codex/config.go:30",
		"internal/sandbox/env.go:15", "internal/sandbox/refused.go:32",
	},
	"layers": {
		"internal/tsnet/config.go:3", "internal/tsnet/off.go:3", "internal/tsnet/tsnet.go:3",
	},
}

// sameWords are phrases of deck's dictionary one translation rightly says alike, in sorted order.
var sameWords = [][]string{
	{"Address", "url"},
	{"Module", "project"},
	{"Work", "role"},
	{"done", "ready"},
	{"enabled", "on"},
	{"missing", "not installed"},
}
