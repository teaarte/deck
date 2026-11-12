package lint

// exceptions are the places that break a rule today, by file:line; the lists only shrink.
var exceptions = map[string][]string{
	"now": {
		"internal/app/asks/process_asks.go:80", "internal/app/asks/process_asks.go:84",
		"internal/app/asks/process_asks.go:86", "internal/app/config.go:61", "internal/app/files/thumbs.go:31",
		"internal/app/files/thumbs.go:32", "internal/app/processes/processes.go:325",
		"internal/app/processes/processes.go:329", "internal/app/processes/processes.go:322",
		"internal/models/hops.go:200", "internal/models/hops.go:210",
		"internal/models/meter.go:23", "internal/models/meter.go:32",
		"internal/provider/acp/acp.go:189",
		"internal/tools/oauth.go:79", "internal/tools/oauth.go:90",
	},
	"exec": {
		"internal/app/updates/updates.go:19", "internal/provider/codex/gen/main.go:11",
		"internal/script/run.go:14", "internal/script/runtime.go:7",
	},
	"read": {
		"cmd/deck-release/main.go:60", "internal/app/feed/push.go:66",
		"internal/app/files/files.go:251", "internal/app/files/light.go:19", "internal/app/files/resource.go:72",
		"internal/app/files/thumbs.go:87", "internal/app/files/thumbs.go:99", "internal/app/project/module_data.go:336",
		"internal/app/project/module_zip.go:41", "internal/app/secrets/move.go:100", "internal/app/secrets/move.go:68", "internal/app/snapshots/snapshot.go:126",
		"internal/app/secrets/secrets.go:124", "internal/app/secrets/secrets.go:52", "internal/app/secrets/secrets.go:77",
		"internal/app/updates/updates.go:345", "internal/app/updates/updates.go:360",
		"internal/app/work/option_files.go:168", "internal/auth/codes.go:68", "internal/auth/origins.go:86",
		"internal/files/files.go:50", "internal/node/allow.go:74", "internal/node/join.go:70", "internal/node/join.go:91",
		"internal/process/input.go:192", "internal/process/log.go:142", "internal/provider/codex/codex.go:179",
		"internal/provider/codex/codex.go:181", "internal/provider/codex/codex.go:201",
		"internal/provider/codex/gen/main.go:111", "internal/provider/home.go:96", "internal/provider/program.go:154",
		"internal/release/release.go:192", "internal/sandbox/bwrap_linux.go:66", "internal/sandbox/path.go:125",
		"internal/sandbox/path.go:152", "internal/sandbox/path.go:96", "internal/tools/browser.go:118",
	},
	"write": {
		"cmd/deck-release/main.go:53", "cmd/deck-release/main.go:56", "cmd/deck/main.go:60", "cmd/deck/main.go:276",
		"internal/app/snapshots/snapshot.go:158", "internal/app/updates/updates.go:329", "internal/process/settle.go:125",
		"internal/provider/codex/gen/main.go:93", "internal/provider/codex/gen/main.go:104",
		"internal/provider/shellcheck.go:80", "internal/provider/shellcheck.go:81", "internal/script/run.go:72",
	},
	"env": {
		"cmd/deck-release/main.go:33", "internal/provider/codex/config.go:30",
		"internal/sandbox/env.go:15", "internal/sandbox/refused.go:32",
	},
	"failure": {
		"internal/app/owners/devices.go:236", "internal/app/project/import.go:133",
		"internal/app/project/module_data.go:285", "internal/app/project/project_delete.go:39",
		"internal/app/providers/selfcheck.go:336", "internal/app/providers/selfcheck.go:338",
		"internal/app/providers/sessions.go:51", "internal/app/secrets/move.go:38",
		"internal/app/signins/agent_login.go:168", "internal/auth/auth.go:81", "internal/auth/browser.go:46",
		"internal/auth/codes.go:47", "internal/auth/passkey.go:58", "internal/egress/node.go:50",
		"internal/egress/node.go:79", "internal/egress/node.go:82", "internal/files/files.go:105",
		"internal/files/files.go:109", "internal/files/files.go:115", "internal/files/files.go:126",
		"internal/files/light.go:43", "internal/files/light.go:57", "internal/home/home.go:134",
		"internal/models/hops.go:357", "internal/node/hub.go:262", "internal/tools/body.go:99",
		"internal/tools/browser.go:288", "internal/tools/content.go:35", "internal/tools/content.go:37",
		"internal/tools/content.go:92", "internal/tools/http.go:180", "internal/tools/http.go:185",
		"internal/tools/http.go:195", "internal/tools/http.go:188", "internal/tools/http.go:203",
		"internal/tools/http.go:100", "internal/tools/oauth.go:167", "internal/tools/oauth.go:171",
		"internal/tools/oauth.go:97",
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
