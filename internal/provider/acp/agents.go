package acp

import (
	"encoding/json"
	"path/filepath"

	"deck2/internal/provider"
)

// Launch is what a run gives the profile of an agent to start it from.
type Launch struct {
	Binary, Home string
	// ModelURL is deck's proxy to the model API; the agent sends Key, the proxy puts the real key in.
	ModelURL, Model, Key string
}

// Agent is how one ACP agent starts on a model of deck, alone in its home.
type Agent struct {
	Start func(l Launch) (argv, env []string, files map[string]string)
	// Skills is the folder in the home where the agent finds skills, so none is written in the folder it works in.
	Skills string
}

// Agents are the ACP agents deck starts; docs/spikes/acp.md has what each one does.
var Agents = map[string]Agent{"goose": {Start: goose, Skills: ".agents/skills"}, "dsh": {Start: dsh, Skills: ".dsh/skills"}}

// gooseTools are the built-in extensions of a session of deck: files and shell, and skills; the rest (sub-agents,
// schedules, apps) outlive or leave the turn.
const gooseTools = `{"developer": {"enabled": true, "type": "platform", "name": "developer", "description": "", "bundled": true},
 "skills": {"enabled": true, "type": "platform", "name": "skills", "description": "", "bundled": true}}`

func goose(l Launch) ([]string, []string, map[string]string) {
	env := []string{"GOOSE_PROVIDER=openai", "GOOSE_MODEL=" + l.Model, "OPENAI_BASE_URL=" + l.ModelURL, "OPENAI_API_KEY=" + l.Key,
		"GOOSE_MODE=auto", "GOOSE_DISABLE_KEYRING=1", "GOOSE_TELEMETRY_OFF=1", "EXTENSIONS=" + gooseTools}
	return []string{l.Binary, "acp"}, env, nil
}

// dshOff are the plugins of the acp profile a session of deck runs without: web tools that need a DeepSeek key,
// background work that outlives the turn, telemetry and a question tool that has no answerer in ACP.
var dshOff = []string{"tool-web", "web", "web-search-deepseek", "web-fetch-http", "session-telemetry-otel", "user-questions",
	"tool-jobs", "tool-workflow", "tool-ralph", "goal", "goal-round-driver", "command-goal", "tool-goal"}

func dsh(l Launch) ([]string, []string, map[string]string) {
	patch := []any{
		map[string]any{"id": "llm-pi-ai", "config": map[string]any{"providers": map[string]any{"deck": map[string]any{
			"api": "openai-completions", "apiKeyEnv": "DECK_MODEL_KEY", "baseURL": l.ModelURL,
			"models": []any{map[string]any{"id": l.Model}}}}}},
		map[string]any{"id": "acp", "config": map[string]any{"provider": "deck", "model": l.Model}},
	}
	for _, id := range dshOff {
		patch = append(patch, map[string]any{"id": id, "disabled": true})
	}
	b, _ := json.Marshal(patch)
	file := filepath.Join(l.Home, "deck.patch.yml")
	env := []string{"DSH_HOME=" + filepath.Join(l.Home, ".dsh"), "DSH_PERMISSION_MODE=danger-full-access",
		"DSH_TELEMETRY_DISABLED=1", "DECK_MODEL_KEY=" + l.Key}
	return []string{l.Binary, "--profile", "acp", "--patch", file}, env, map[string]string{file: string(b)}
}

// caps are what every ACP agent does inside the sandbox of deck.
func caps(noCloud bool) provider.Caps {
	c := provider.Caps{provider.Tools, provider.Sandbox, provider.Resume, provider.Intercept, provider.Folder}
	if noCloud {
		c = append(c, provider.NoCloud)
	}
	return c
}
