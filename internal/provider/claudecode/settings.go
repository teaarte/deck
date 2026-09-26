package claudecode

import (
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// webTools are the CLI's own network tools; a project without network does not get them.
var webTools = []string{"WebFetch", "WebSearch"}

// offlineTools are the only built-in tools of a project without network, so a network tool of a later CLI is off too.
var offlineTools = []string{"Bash", "Read", "Glob", "Grep", "Edit", "Write", "NotebookEdit", "TodoWrite", "Skill", "Task", "AskUserQuestion"}

var envNames = map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"LANG": true, "TZ": true, "TMPDIR": true}

var envPrefixes = []string{"LC_", "XDG_", "NODE_"}

// environ keeps only the allowlisted variables, so login and API selection variables never reach the CLI.
func environ(parent []string) []string {
	var out []string
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		ok := envNames[name]
		for _, p := range envPrefixes {
			ok = ok || strings.HasPrefix(name, p)
		}
		if ok {
			out = append(out, kv)
		}
	}
	return append(out, "MCP_TOOL_TIMEOUT=2400000")
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
	for _, root := range paths(req.Cwd) {
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
	f, err := os.CreateTemp(a.ConfigDir, pattern+"-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	final := strings.TrimSuffix(f.Name(), ".tmp") + ".json"
	if werr == nil {
		werr = os.Rename(f.Name(), final)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	return final, nil
}

// paths gives a folder by its absolute and its real path.
func paths(dir string) []string {
	var out []string
	if abs, err := filepath.Abs(dir); err == nil {
		out = append(out, abs)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil && !slices.Contains(out, real) {
		out = append(out, real)
	}
	return out
}

// excludes lists the instruction files of every folder above the repo, by the given and the real path.
func excludes(repo string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range paths(repo) {
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
	for path, text := range files {
		f := filepath.Join(dir, path)
		if !strings.HasPrefix(f, dir+string(filepath.Separator)) {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("claude-code: skill file %q is outside the plugin", path)
		}
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		if err := os.WriteFile(f, []byte(text), 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}
