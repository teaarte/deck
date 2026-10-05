package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"deck2/internal/provider"
	"deck2/internal/sandbox"
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

// cliState hides the owner's Claude Code folder but the folder of this session, where the CLI keeps the tool
// output it asks the agent to read: what is there now, and the folders of every other run of deck.
func cliState(dir, runs string, req provider.Request) []string {
	own := map[string]bool{}
	for _, c := range provider.Paths(req.Cwd) {
		own[nonWord.ReplaceAllString(c, "-")] = true
	}
	var out []string
	hide := func(p string, dir bool) {
		if dir {
			p += "/**"
		}
		out = append(out, p)
	}
	run := ""
	for _, r := range provider.Paths(runs) {
		if rel, err := filepath.Rel(r, req.Cwd); err == nil && filepath.Dir(rel) != "." && !strings.HasPrefix(rel, "..") {
			run = strings.Split(rel, string(filepath.Separator))[0]
		}
	}
	for _, r := range provider.Paths(runs) {
		prefix := nonWord.ReplaceAllString(r, "-") + "-"
		names := []string{prefix + "?*-work"}
		if run != "" {
			names = sandbox.Others(prefix, run, "-work")
		}
		for _, n := range names {
			hide(filepath.Join(dir, "projects", n), true)
		}
	}
	top, _ := os.ReadDir(dir)
	for _, e := range top {
		if e.Name() != "projects" {
			hide(filepath.Join(dir, e.Name()), e.IsDir())
		}
	}
	projects, _ := os.ReadDir(filepath.Join(dir, "projects"))
	for _, e := range projects {
		if !own[e.Name()] {
			hide(filepath.Join(dir, "projects", e.Name()), e.IsDir())
			continue
		}
		sessions, _ := os.ReadDir(filepath.Join(dir, "projects", e.Name()))
		for _, f := range sessions {
			if n := f.Name(); n != req.SessionID && n != req.SessionID+".jsonl" {
				hide(filepath.Join(dir, "projects", e.Name(), n), f.IsDir())
			}
		}
	}
	return out
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9]`)

// settings are passed by flag, so they win over the settings of the repo.
func settings(req provider.Request, runs string) map[string]any {
	env := map[string]string{}
	for _, name := range apiVariables {
		env[name] = ""
	}
	s := map[string]any{"apiKeyHelper": "", "env": env}
	var deny []string
	hidden := slices.Clone(req.Hidden)
	for _, p := range sandbox.Private() {
		if filepath.Base(p) == ".claude" {
			hidden = append(hidden, cliState(p, runs, req)...)
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
