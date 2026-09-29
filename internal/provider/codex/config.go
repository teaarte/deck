package codex

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/provider"
)

var envNames = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TZ", "TMPDIR", "CODEX_HOME"}

// environ keeps only the allowlisted variables, so API keys and provider selection never reach Codex;
// CODEX_HOME stays, since it is where the owner's shared login lives.
func environ(parent []string) []string { return provider.Environ(parent, envNames, "LC_", "XDG_") }

// codexHome is the owner's Codex folder: the agent's shell may not read it, the login and history live there.
func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".codex")
}

// off are the features of the owner's Codex a session of deck runs without.
var off = []string{"features.plugins=false", "features.apps=false", "features.hooks=false", "features.memories=false",
	"features.browser_use=false", "features.computer_use=false", "features.image_generation=false", "features.goals=false",
	"features.exec_permission_approvals=false", "agents.enabled=false", "notify=[]"}

// quiet are the notifications deck does not read.
var quiet = []string{"item/agentMessage/delta", "item/plan/delta", "item/reasoning/summaryTextDelta",
	"item/reasoning/summaryPartAdded", "item/reasoning/textDelta", "item/commandExecution/outputDelta",
	"item/fileChange/outputDelta", "item/fileChange/patchUpdated", "command/exec/outputDelta", "process/outputDelta",
	"turn/diff/updated", "item/mcpToolCall/progress"}

// profile is the name of the permission profile a turn of deck runs under.
const profile = "deck"

// args start the app-server without the owner's plugins, hooks and the like, and with the profile of the turn:
// the shell reads all but deck's and the owner's Codex folder and writes its own; the deepest path wins.
func (a *Adapter) args(req provider.Request, skills string) ([]string, error) {
	fs := map[string]string{":root": "read", ":tmpdir": "write", ":slash_tmp": "write"}
	for _, p := range append(provider.Paths(a.Home), provider.Paths(codexHome())...) {
		fs[p] = "deny"
	}
	for _, h := range req.Hidden {
		if !provider.Under(h, a.Home) {
			return nil, fmt.Errorf("codex: hidden path %s is outside deck's folder, which is all the profile hides", h)
		}
	}
	writes := append([]string{req.Cwd}, req.Dirs...)
	if req.Repo {
		writes = append(writes, gitDirs(req.Cwd)...)
	}
	for _, d := range writes {
		for _, p := range provider.Paths(d) {
			fs[p] = "write"
		}
	}
	if skills != "" {
		for _, p := range provider.Paths(skills) {
			fs[p] = "read"
		}
	}
	keys := make([]string, 0, len(fs))
	for k := range fs {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var table []string
	for _, k := range keys {
		table = append(table, quote(k)+"="+quote(fs[k]))
	}
	web := "live"
	if req.NoNetwork {
		web = "disabled"
	}
	args := []string{"app-server"}
	for _, kv := range append(slices.Clone(off),
		"web_search="+quote(web),
		"default_permissions="+quote(":read-only"),
		"permissions."+profile+".description="+quote("deck session "+req.SessionID),
		"permissions."+profile+".filesystem={"+strings.Join(table, ",")+"}",
		fmt.Sprintf("permissions.%s.network.enabled=%t", profile, !req.NoNetwork)) {
		args = append(args, "-c", kv)
	}
	return args, nil
}

// gitDirs is the .git folder of a repo, which Codex may keep read-only in a folder it writes. A .git file or link
// is left out: it names a folder elsewhere, and the agent could have written it.
func gitDirs(repo string) []string {
	dot := filepath.Join(repo, ".git")
	if fi, err := os.Lstat(dot); err != nil || !fi.IsDir() {
		return nil
	}
	return []string{dot}
}

// quote writes a TOML basic string; JSON escapes are valid TOML.
func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// threadConfig turns the owner's MCP servers and skills off for the thread and adds the deck gateway, whose
// class rules decide about its tools, so Codex does not ask about them itself.
func threadConfig(req provider.Request, servers []string, skillsOff []string) map[string]any {
	mcp := map[string]any{}
	for _, s := range servers {
		mcp[s] = map[string]any{"enabled": false}
	}
	mcp[server] = map[string]any{"url": req.MCPURL, "default_tools_approval_mode": "approve",
		"disabled_tools": []string{approveTool(req)}, "tool_timeout_sec": 2400}
	cfg := map[string]any{"mcp_servers": mcp}
	if len(skillsOff) > 0 {
		var rules []map[string]any
		for _, p := range skillsOff {
			rules = append(rules, map[string]any{"path": p, "enabled": false})
		}
		cfg["skills"] = map[string]any{"config": rules}
	}
	return cfg
}

const server = provider.Server

// approveTool is the gateway's permission tool as the gateway names it; the model does not see it.
func approveTool(req provider.Request) string {
	return strings.TrimPrefix(req.PermissionMCP, "mcp__"+server+"__")
}
