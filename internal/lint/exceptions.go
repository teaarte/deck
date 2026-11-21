package lint

// exceptions are the places that break a rule today, by file:line; the lists only shrink.
var exceptions = map[string][]string{
	// The trial of a command is the run of deck.command_try, which the gateway decided.
	"handler": {"internal/app/project/drafts.go:95"},
	"exec": {
		"internal/app/updates/updates.go:18", "internal/provider/codex/gen/main.go:11",
		"internal/script/runtime.go:7",
	},
	"read": {
		"cmd/deck-release/main.go:60", "internal/app/feed/push.go:67",
		"internal/app/files/files.go:249", "internal/app/files/light.go:19", "internal/app/files/resource.go:72",
		"internal/app/files/thumbs.go:88", "internal/app/files/thumbs.go:100", "internal/app/modules/module_data.go:338",
		"internal/app/modules/module_zip.go:41", "internal/app/secrets/move.go:100", "internal/app/secrets/move.go:68", "internal/app/snapshots/snapshot.go:129",
		"internal/app/secrets/secrets.go:124", "internal/app/secrets/secrets.go:52", "internal/app/secrets/secrets.go:77",
		"internal/app/updates/updates.go:345", "internal/app/updates/updates.go:360",
		"internal/app/models/option_files.go:168", "internal/auth/codes.go:68", "internal/auth/origins.go:86",
		"internal/files/files.go:50", "internal/node/allow.go:74", "internal/node/join.go:70", "internal/node/join.go:91",
		"internal/process/input.go:192", "internal/process/log.go:145", "internal/provider/codex/codex.go:197",
		"internal/provider/codex/codex.go:199", "internal/provider/codex/codex.go:228",
		"internal/provider/codex/gen/main.go:111", "internal/provider/home.go:113", "internal/provider/program.go:154",
		"internal/release/release.go:194", "internal/sandbox/bwrap_linux.go:66", "internal/sandbox/path.go:125",
		"internal/sandbox/path.go:152", "internal/sandbox/path.go:96", "internal/tools/browser.go:116",
	},
	"write": {
		"cmd/deck-release/main.go:53", "cmd/deck-release/main.go:56", "cmd/deck/main.go:60", "cmd/deck/main.go:281",
		"internal/app/snapshots/snapshot.go:168", "internal/app/updates/updates.go:329", "internal/process/settle.go:119",
		"internal/provider/codex/gen/main.go:93", "internal/provider/codex/gen/main.go:104",
		"internal/provider/shellcheck.go:80", "internal/provider/shellcheck.go:81",
	},
	"env": {
		"cmd/deck-release/main.go:33", "internal/provider/codex/config.go:30",
		"internal/sandbox/env.go:15", "internal/sandbox/refused.go:32",
	},
	"dropped": {
		"internal/app/asks/consent.go:45", "internal/app/asks/consent.go:102", "internal/app/asks/heard.go:46",
		"internal/app/asks/heard.go:109", "internal/app/feed/push.go:193",
		"internal/app/feed/push.go:195", "internal/app/feed/push.go:320", "internal/app/files/files.go:203",
		"internal/app/files/outbox.go:37", "internal/app/files/outbox.go:40", "internal/app/processes/failures.go:123",
		"internal/app/processes/failures.go:177", "internal/app/providers/agents.go:17", "internal/app/providers/agents.go:38",
		"internal/app/providers/limit.go:147", "internal/app/providers/limit.go:218",
		"internal/app/providers/limit.go:259", "internal/app/providers/limit.go:287",
		"internal/app/providers/selfcheck.go:68", "internal/app/providers/selfcheck.go:124",
		"internal/app/models/option_files.go:133", "internal/app/models/spend.go:80",
		"internal/app/models/transcribe.go:39", "internal/auth/auth.go:113", "internal/auth/auth.go:117",
		"internal/gateway/approve.go:178", "internal/gateway/approve.go:276",
		"internal/gateway/gate.go:279", "internal/gateway/gate.go:312",
		"internal/gateway/press.go:70",
		"internal/process/files.go:243", "internal/process/health.go:33", "internal/process/log.go:72",
		"internal/process/park.go:101", "internal/process/resume.go:123", "internal/process/resume.go:255",
		"internal/process/script.go:180", "internal/process/submit.go:57", "internal/process/tool.go:142",
		"internal/process/turn.go:304", "internal/process/wake.go:59", "internal/process/wake.go:185",
		"internal/process/wake.go:202", "internal/process/wake.go:207",
	},
	"failure": {
		"internal/app/owners/devices.go:235", "internal/app/project/import.go:126",
		"internal/app/modules/module_data.go:288", "internal/app/project/project_delete.go:39",
		"internal/app/providers/selfcheck.go:379", "internal/app/providers/selfcheck.go:381",
		"internal/app/providers/sessions.go:51", "internal/app/secrets/move.go:38",
		"internal/app/signins/agent_login.go:168", "internal/auth/auth.go:82", "internal/auth/browser.go:46",
		"internal/auth/codes.go:47", "internal/auth/passkey.go:58", "internal/egress/node.go:60",
		"internal/egress/node.go:91", "internal/egress/node.go:94", "internal/files/files.go:105",
		"internal/files/files.go:109", "internal/files/files.go:115", "internal/files/files.go:126",
		"internal/files/light.go:43", "internal/files/light.go:57", "internal/home/home.go:140",
		"internal/models/hops.go:405", "internal/node/hub.go:262", "internal/tools/body.go:99",
		"internal/tools/browser.go:286", "internal/tools/content.go:21", "internal/tools/content.go:64",
		"internal/tools/http.go:88", "internal/tools/http.go:162", "internal/tools/http.go:167",
		"internal/tools/http.go:170", "internal/tools/http.go:177", "internal/tools/http.go:185",
		"internal/tools/oauth.go:168", "internal/tools/oauth.go:172",
		"internal/tools/oauth.go:98",
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
