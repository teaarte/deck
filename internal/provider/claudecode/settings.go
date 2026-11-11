package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

// webTools are the CLI's own network tools, which a CLI in a box of its own does not get.
var webTools = []string{"WebFetch", "WebSearch"}

// freeTools run without asking: read-only and planning tools.
var freeTools = []string{"Read", "Glob", "Grep", "TodoWrite", "StructuredOutput", "Skill"}

// apiVariables are login and API selection variables the settings of deck blank out over those of the repo.
var apiVariables = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL",
	"ANTHROPIC_CUSTOM_HEADERS", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
	"CLAUDE_CODE_USE_FOUNDRY", "AWS_BEARER_TOKEN_BEDROCK"}

func apiVariable(name string) bool {
	return slices.Contains(apiVariables, name) || strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_")
}

// cliKeys are the files of the owner's Claude Code folder that hold a login or API keys.
var cliKeys = []string{".credentials.json", "settings.json", "settings.local.json"}

// settings are passed by flag, so they win over the settings of the repo. Outside a box of its own the CLI gets the
// rules that close deck's folder and the owner's keys to its tools, as it did.
func (a *Adapter) settings(req provider.Request) map[string]any {
	env := map[string]string{}
	for _, name := range apiVariables {
		env[name] = ""
	}
	// deck keeps the one memory of every provider; the CLI's own is off, the variable over that of a repo too.
	s := map[string]any{"apiKeyHelper": "", "env": env, "autoMode": autoMode(req), "autoMemoryEnabled": false}
	var deny []string
	if req.Repo {
		blank(req.Cwd, env)
		// The settings of the owner's repo are the owner's: their hooks would run in the owner's own CLI.
		for _, root := range home.Names(req.Cwd) {
			for _, tool := range []string{"Edit", "Write"} {
				deny = append(deny, tool+"(/"+filepath.Join(root, ".claude", "settings*.json")+")")
			}
		}
		s["permissions"] = map[string]any{"deny": deny}
	}
	env["CLAUDE_CODE_DISABLE_AUTO_MEMORY"] = "1"
	if sandbox.Homed() {
		return s
	}
	hidden := sandbox.Agent(a.Home)
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
	// The files given with messages are read only, here too where no box mounts them so.
	for _, g := range req.Given {
		for _, p := range home.Names(g) {
			deny = append(deny, "Edit(/"+p+"/**)")
		}
	}
	s["permissions"] = map[string]any{"deny": deny}
	if req.Repo {
		s["claudeMdExcludes"] = excludes(req.Cwd)
	}
	return s
}

// blank blanks out the login and API variables the settings of a repo set, so the CLI keeps the owner's login.
func blank(repo string, env map[string]string) {
	for _, root := range home.Names(repo) {
		for _, f := range []string{"settings.json", "settings.local.json"} {
			var set struct {
				Env map[string]any `json:"env"`
			}
			if b, err := os.ReadFile(filepath.Join(root, ".claude", f)); err == nil && json.Unmarshal(b, &set) == nil {
				for name := range set.Env {
					if apiVariable(name) {
						env[name] = ""
					}
				}
			}
		}
	}
}

// autoMode tells the classifier of auto mode the folders of the agent, where reading and searching is its work; a
// path outside them it judges as it would.
func autoMode(req provider.Request) map[string]any {
	var folders []string
	for _, d := range slices.Concat([]string{req.Cwd}, req.Dirs, req.Given) {
		for _, p := range home.Names(d) {
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

// excludes lists the instruction files of every folder above the repo, by the given and the real path.
func excludes(repo string) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range home.Names(repo) {
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

// writePlugin puts skills into a plugin folder of the session, at one path while they do not change: with no setting
// sources the CLI reads skills only from plugins.
func writePlugin(h home.Layout, session string, skills map[string]string) (string, error) {
	if len(skills) == 0 {
		return "", nil
	}
	files := map[string]string{".claude-plugin/plugin.json": `{"name":"deck"}`}
	for path, text := range skills {
		files[filepath.Join("skills", path)] = text
	}
	dir, err := provider.Skills(h, cli, session, files)
	if err != nil {
		return "", fmt.Errorf("claude-code: %w", err)
	}
	return dir, nil
}
