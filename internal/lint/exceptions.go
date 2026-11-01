package lint

// exceptions are the places that break a rule today, by file:line; the lists only shrink.
var exceptions = map[string][]string{
	"now": {
		"internal/app/asks/process_asks.go:76", "internal/app/asks/process_asks.go:80",
		"internal/app/asks/process_asks.go:82", "internal/app/config.go:55", "internal/app/files/thumbs.go:31",
		"internal/app/files/thumbs.go:32", "internal/app/processes/processes.go:289",
		"internal/app/processes/processes.go:292", "internal/app/processes/processes.go:296",
		"internal/app/providers/video.go:58", "internal/app/providers/video.go:67", "internal/app/tokens/lent.go:37",
		"internal/app/tokens/lent.go:63", "internal/models/hops.go:149", "internal/models/hops.go:159",
		"internal/models/meter.go:23", "internal/models/meter.go:32", "internal/mcphost/host.go:69",
		"internal/provider/acp/acp.go:198", "internal/script/run.go:216", "internal/script/run.go:220",
		"internal/tools/oauth.go:79", "internal/tools/oauth.go:90",
	},
	"exec": {
		"internal/app/providers/login.go:84", "internal/app/updates/updates.go:18",
		"internal/mcphost/host.go:12", "internal/mcphost/server.go:8", "internal/mcphost/server.go:31",
		"internal/provider/child.go:8", "internal/provider/child.go:32", "internal/provider/claudecode/claudecode.go:13",
		"internal/provider/claudecode/models.go:9", "internal/provider/claudecode/selfcheck.go:9",
		"internal/provider/codex/codex.go:12", "internal/provider/codex/gen/main.go:11",
		"internal/provider/codex/models.go:6", "internal/provider/codex/selfcheck.go:6", "internal/provider/provider.go:8",
		"internal/script/run.go:14", "internal/script/runtime.go:7",
	},
	"write": {
		"cmd/deck-release/main.go:53", "cmd/deck-release/main.go:56", "cmd/deck/main.go:50", "cmd/deck/main.go:237",
		"internal/app/snapshots/snapshot.go:100", "internal/app/updates/updates.go:286", "internal/process/script.go:119",
		"internal/provider/child.go:27", "internal/provider/claudecode/selfcheck.go:98",
		"internal/provider/claudecode/settings.go:134", "internal/provider/codex/gen/main.go:93",
		"internal/provider/codex/gen/main.go:104", "internal/provider/shellcheck.go:74",
		"internal/provider/shellcheck.go:75", "internal/script/run.go:73",
	},
	"env": {
		"cmd/deck-release/main.go:33", "internal/provider/codex/config.go:34", "internal/sandbox/bridge_linux.go:116",
		"internal/sandbox/env.go:14", "internal/sandbox/rules.go:32", "internal/sandbox/user.go:29",
		"internal/sandbox/user.go:65",
	},
	"layers": {
		"internal/tsnet/config.go:3", "internal/tsnet/off.go:3", "internal/tsnet/tsnet.go:3",
	},
	"go": {
		"internal/app/providers/login.go:113", "internal/app/providers/login.go:150",
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
