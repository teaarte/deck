package codex

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

// caches are the variables that put the caches of the programs of its shell in the data folder of the project, as
// those of scripts; Codex sets them in its shell itself.
func caches(data string) string {
	if data == "" {
		return "{}"
	}
	return "{" + strings.Join([]string{"XDG_CACHE_HOME=" + quote(filepath.Join(data, ".cache")),
		"XDG_DATA_HOME=" + quote(filepath.Join(data, ".local", "share")),
		"npm_config_cache=" + quote(filepath.Join(data, ".cache", "npm"))}, ",") + "}"
}

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

// args start the app-server without the owner's plugins and hooks; in a box of its own its sandbox is off and its
// login a file, outside one the profile of the turn bounds it, as it did.
func (a *Adapter) args(req provider.Request, skills string) ([]string, error) {
	var perms []string
	switch {
	case sandbox.Homed():
		perms = []string{"sandbox_mode=" + quote("danger-full-access"), "cli_auth_credentials_store=" + quote("file")}
	case sandbox.Contained():
		// In deck's container its user of agents is the sandbox; Codex's own needs namespaces the container has not.
		perms = []string{"sandbox_mode=" + quote("danger-full-access")}
	default:
		perms = a.profile(req, skills)
	}
	web := "live"
	if sandbox.Homed() {
		// The search of Codex runs on the provider's side, past the door of the box.
		web = "disabled"
	}
	args := []string{"app-server"}
	for _, kv := range slices.Concat(off, []string{"web_search=" + quote(web)}, perms, []string{"shell_environment_policy.set=" + caches(req.Data)}) {
		args = append(args, "-c", kv)
	}
	return args, nil
}

// profile is the permission profile of a turn outside a box of its own: the shell reads all but what deck shuts from
// agents and the owner's Codex folder, and writes its own; the deepest path wins.
func (a *Adapter) profile(req provider.Request, skills string) []string {
	fs := map[string]string{":root": "read", ":tmpdir": "write", ":slash_tmp": "write"}
	for _, d := range slices.Concat(sandbox.Shut(a.Home), []string{codexHome()}, sandbox.Private()) {
		for _, p := range home.Names(d) {
			fs[p] = "deny"
		}
	}
	// bwrap keeps a read path inside a denied one closed, and Codex runs its sandbox helper from tmp of its folder.
	if runtime.GOOS == "linux" {
		for _, p := range home.Names(codexHome()) {
			delete(fs, p)
			fs[filepath.Join(p, "{[!t]*,t[!m]*,tm[!p]*,tmp?*,t,tm}")] = "deny"
		}
	}
	writes := append([]string{req.Cwd}, req.Dirs...)
	if req.Repo {
		writes = append(writes, gitDirs(req.Cwd)...)
	}
	for _, d := range writes {
		for _, p := range home.Names(d) {
			fs[p] = "write"
		}
	}
	if skills != "" {
		for _, p := range home.Names(skills) {
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
	return []string{"default_permissions=" + quote(":read-only"),
		"permissions." + profile + ".description=" + quote("deck session "+req.SessionID),
		"permissions." + profile + ".filesystem={" + strings.Join(table, ",") + "}",
		"permissions." + profile + ".network.enabled=true"}
}

// modelHosts are the hosts of the model providers a config of Codex names by base_url.
func modelHosts(cfg string) []string {
	var out []string
	for _, line := range strings.Split(cfg, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "base_url" {
			continue
		}
		var raw string
		if json.Unmarshal([]byte(strings.TrimSpace(v)), &raw) != nil {
			raw = strings.Trim(strings.TrimSpace(v), `'"`)
		}
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			out = append(out, u.Host)
		}
	}
	return out
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

// threadConfig turns the owner's MCP servers and skills off for the thread and adds the deck gateway at the door of
// the box, whose class rules decide about its tools, so Codex does not ask about them itself.
func threadConfig(req provider.Request, door string, servers []string, skillsOff []string) map[string]any {
	mcp := map[string]any{}
	for _, s := range servers {
		mcp[s] = map[string]any{"enabled": false}
	}
	mcp[server] = map[string]any{"url": door, "default_tools_approval_mode": "approve",
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
	return provider.Bare(req.PermissionMCP)
}
