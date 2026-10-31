package lint

// exceptions are the places that break a rule today, by file:line; the lists only shrink.
var exceptions = map[string][]string{
	"now": {
		"internal/app/asks/process_asks.go:76", "internal/app/asks/process_asks.go:80",
		"internal/app/asks/process_asks.go:82", "internal/app/config.go:55", "internal/app/files/thumbs.go:31",
		"internal/app/files/thumbs.go:32", "internal/app/processes/processes.go:288",
		"internal/app/processes/processes.go:291", "internal/app/processes/processes.go:295",
		"internal/app/providers/video.go:58", "internal/app/providers/video.go:67", "internal/app/tokens/lent.go:37",
		"internal/app/tokens/lent.go:63", "internal/gateway/model_hops.go:145", "internal/gateway/model_hops.go:155",
		"internal/gateway/model_meter.go:23", "internal/gateway/model_meter.go:32", "internal/mcphost/host.go:71",
		"internal/provider/acp/acp.go:207", "internal/script/run.go:216", "internal/script/run.go:220",
		"internal/tools/oauth.go:91", "internal/tools/oauth.go:102",
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
