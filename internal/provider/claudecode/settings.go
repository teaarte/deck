package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/provider"
)

// webTools are the CLI's own network tools; a project without network does not get them.
var webTools = []string{"WebFetch", "WebSearch"}

// offlineTools are the only built-in tools of a project without network, so a network tool of a later CLI is off too.
var offlineTools = []string{"Bash", "Read", "Glob", "Grep", "Edit", "Write", "NotebookEdit", "TodoWrite", "Skill", "Task", "AskUserQuestion"}

var envNames = []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "LANG", "TZ", "TMPDIR"}

// environ keeps only the allowlisted variables, so login and API selection variables never reach the CLI.
func environ(parent []string) []string {
	return append(provider.Environ(parent, envNames, "LC_", "XDG_", "NODE_"), "MCP_TOOL_TIMEOUT=2400000")
}

// apiVariables are login and API selection variables the settings of deck blank out over those of the repo.
var apiVariables = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
	"ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY", "AWS_BEARER_TOKEN_BEDROCK"}

func apiVariable(name string) bool {
	return slices.Contains(apiVariables, name) || strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_")
}

// settings are passed by flag, so they win over the settings of the repo.
func settings(req provider.Request) map[string]any {
	env := map[string]string{}
	for _, name := range apiVariables {
		env[name] = ""
	}
	s := map[string]any{"apiKeyHelper": "", "env": env}
	var deny []string
	for _, h := range req.Hidden {
		for _, tool := range []string{"Read", "Grep", "Glob", "Edit", "Write"} {
			deny = append(deny, tool+"(/"+h+")")
		}
	}
	s["permissions"] = map[string]any{"deny": deny}
	if req.NoNetwork {
		s["sandbox"] = map[string]any{"enabled": true, "failIfUnavailable": true, "allowUnsandboxedCommands": false,
			"network": map[string]any{"allowedDomains": []string{}, "strictAllowlist": true}}
	}
	if !req.Repo {
		return s
	}
	for _, root := range provider.Paths(req.Cwd) {
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
	for _, root := range provider.Paths(repo) {
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
