package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/paths"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

// webTools are the CLI's own network tools; a project without network does not get them.
var webTools = []string{"WebFetch", "WebSearch"}

// freeTools run without asking: read-only and planning tools.
var freeTools = []string{"Read", "Glob", "Grep", "TodoWrite", "StructuredOutput", "Skill"}

// offlineTools are the only built-in tools of a project without network, so a network tool of a later CLI is off too.
var offlineTools = []string{"Bash", "Read", "Glob", "Grep", "Edit", "Write", "NotebookEdit", "TodoWrite", "Skill", "Task", "AskUserQuestion"}

// environ keeps only the allowlisted variables, so login and API selection variables never reach the CLI.
func environ() []string {
	return append(sandbox.Kept("HOME", "TMPDIR", "XDG_", "NODE_"), "MCP_TOOL_TIMEOUT=2400000")
}

// apiVariables are login and API selection variables the settings of deck blank out over those of the repo.
var apiVariables = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
	"ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY", "AWS_BEARER_TOKEN_BEDROCK"}

func apiVariable(name string) bool {
	return slices.Contains(apiVariables, name) || strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_")
}

// cliKeys are the files of the owner's Claude Code folder that hold a login or API keys.
var cliKeys = []string{".credentials.json", "settings.json", "settings.local.json"}

// settings are passed by flag, so they win over the settings of the repo.
func settings(req provider.Request) map[string]any {
	env := map[string]string{}
	for _, name := range apiVariables {
		env[name] = ""
	}
	s := map[string]any{"apiKeyHelper": "", "env": env, "autoMode": autoMode(req)}
	var deny []string
	hidden := slices.Clone(req.Hidden)
	for _, p := range sandbox.Private() {
		if filepath.Base(p) == ".claude" {
			// The CLI keeps the long tool output it asks the agent to read in its own folder, so only its keys close.
			for _, f := range cliKeys {
				hidden = append(hidden, filepath.Join(p, f))
			}
			continue
		}
		hidden = append(hidden, p, p+"/**")
	}
	// Read rules hold for every tool that reads files and Edit rules for every one that edits them; a longer list
	// of rules, thousands of them, makes the CLI refuse its own web and shell tools.
	for _, h := range hidden {
		for _, tool := range []string{"Read", "Edit"} {
			deny = append(deny, tool+"(/"+h+")")
		}
	}
	s["permissions"] = map[string]any{"deny": deny}
	// The sandbox of the CLI closes the shell of a project without network only: with it on, a shell binds no port,
	// gets no GPU and writes no cache outside its folders, which renders and local models need.
	if req.NoNetwork {
		s["sandbox"] = map[string]any{"enabled": true, "failIfUnavailable": true, "allowUnsandboxedCommands": false,
			"network": map[string]any{"allowedDomains": []string{}, "strictAllowlist": true}}
	}
	if !req.Repo {
		return s
	}
	for _, root := range paths.Names(req.Cwd) {
		for _, f := range []string{"settings.json", "settings.local.json"} {
			var repo struct {
				Env map[string]any `json:"env"`
			}
			if b, err := os.ReadFile(filepath.Join(root, ".claude", f)); err == nil && json.Unmarshal(b, &repo) == nil {
				for name := range repo.Env {
					if apiVariable(name) {
						env[name] = ""
					}
				}
			}
		}
		for _, tool := range []string{"Edit", "Write"} {
			deny = append(deny, tool+"(/"+filepath.Join(root, ".claude", "settings*.json")+")")
		}
	}
	s["claudeMdExcludes"] = excludes(req.Cwd)
	s["permissions"] = map[string]any{"deny": deny}
	return s
}

// autoMode tells the classifier of auto mode the folders of the agent, where reading and searching is its work; a
// path outside them it judges as it would.
func autoMode(req provider.Request) map[string]any {
	var folders []string
	for _, d := range append([]string{req.Cwd}, req.Dirs...) {
		for _, p := range paths.Names(d) {
			if !slices.Contains(folders, p) {
				folders = append(folders, p)
			}
		}
	}
	list := strings.Join(folders, ", ")
	return map[string]any{
		"environment": []string{"$defaults", "**Agent folders**: this session is an agent deck runs in its own folders: " + list +
			". They are its working folders though they lie under deck's home; every path outside them, deck's home among them, is not the agent's."},
		"allow": []string{"$defaults", "Agent Folder Reads: reading, searching and comparing files with read-only commands " +
			"(grep, rg, cat, head, tail, wc, diff, ls, sort, cut, find without -exec or -delete) when every path they read is inside " +
			"the agent folders of Environment is the agent's work, not Credential Exploration or scope escalation; a path outside them is not covered."},
	}
}

// writeFile writes a per-turn file into ConfigDir atomically with mode 600.
func (a *Adapter) writeFile(pattern string, data []byte) (string, error) {
	if err := os.MkdirAll(a.ConfigDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(a.ConfigDir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(a.ConfigDir, pattern+"-*.json")
	if err != nil {
		return "", err
	}
	_ = f.Close()
	return f.Name(), provider.WriteFile(f.Name(), data)
}

// excludes lists the instruction files of every folder above the repo, by the given and the real path.
func excludes(repo string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range paths.Names(repo) {
		for d := filepath.Dir(root); ; d = filepath.Dir(d) {
			for _, f := range []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/rules/**"} {
				if p := filepath.Join(d, f); !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
			}
			if d == filepath.Dir(d) {
				break
			}
		}
	}
	return out
}

// writePlugin puts skills into a plugin folder next to the MCP config: with no setting sources the CLI reads
// skills only from plugins, and the folder stays outside every folder the agent writes to.
func (a *Adapter) writePlugin(skills map[string]string) (string, error) {
	if len(skills) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(a.ConfigDir, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(a.ConfigDir, "plugin-*")
	if err != nil {
		return "", err
	}
	files := map[string]string{".claude-plugin/plugin.json": `{"name":"deck"}`}
	for path, text := range skills {
		files[filepath.Join("skills", path)] = text
	}
	if err := provider.WriteTree(dir, files, provider.WritePrivate); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("claude-code: %w", err)
	}
	return dir, nil
}
