package acp

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"deck2/internal/provider"
)

// Launch is what a run gives the profile of an agent to start it from.
type Launch struct {
	Binary, Home string
	// ModelURL is deck's proxy to the model API; the agent sends Key, the proxy puts the real key in.
	ModelURL, Model, Key string
	// Port is a free local port for an agent that serves itself over HTTP.
	Port      int
	NoNetwork bool
}

// Agent is how one ACP agent starts on a model of deck, alone in its home.
type Agent struct {
	// Web is set when the agent has its own web fetch or search.
	Web   bool
	Start func(l Launch) (argv, env []string, files map[string]string)
	// Major is the major version deck runs, any when zero; Back says how to install it.
	Major int
	Back  string
}

// Agents are the ACP agents deck starts; docs/spikes/acp.md has what each one does.
var Agents = map[string]Agent{"opencode": {Web: true, Start: opencode, Major: 1, Back: "npm i -g opencode-ai@1.18.30"}, "dsh": {Start: dsh}}

// Supports refuses a version of the agent deck does not run, such as "opencode 2.0.20", with how to go back.
func (g Agent) Supports(version string) error {
	_, v, _ := strings.Cut(version, " ")
	major, _, _ := strings.Cut(v, ".")
	if g.Major == 0 || major == strconv.Itoa(g.Major) {
		return nil
	}
	return fmt.Errorf("%s is not supported: deck runs %d.x; install it with %s", version, g.Major, g.Back)
}

func opencode(l Launch) ([]string, []string, map[string]string) {
	perm := map[string]any{"*": "allow", "question": "deny"}
	if l.NoNetwork {
		perm["webfetch"], perm["websearch"] = "deny", "deny"
	}
	cfg, _ := json.Marshal(map[string]any{
		"provider": map[string]any{"deck": map[string]any{"npm": "@ai-sdk/openai-compatible", "name": "deck",
			"options": map[string]any{"baseURL": l.ModelURL, "apiKey": l.Key, "includeUsage": true},
			"models":  map[string]any{l.Model: map[string]any{}}}},
		"model": "deck/" + l.Model, "small_model": "deck/" + l.Model, "permission": perm,
		"autoupdate": false, "share": "disabled"})
	env := []string{"OPENCODE_CONFIG_CONTENT=" + string(cfg), "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_DISABLE_AUTOUPDATE=1",
		"OPENCODE_DISABLE_LSP_DOWNLOAD=1", "OPENCODE_DISABLE_PROJECT_CONFIG=1", "OPENCODE_DISABLE_CLAUDE_CODE=1"}
	return []string{l.Binary, "acp", "--pure", "--port", fmt.Sprint(l.Port)}, env, nil
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

// caps are what every ACP agent does inside the sandbox of deck, and its own web when it has one.
func (g Agent) caps(noCloud bool) provider.Caps {
	c := provider.Caps{provider.Tools, provider.Sandbox, provider.Resume, provider.Intercept}
	if g.Web {
		c = append(c, provider.Web)
	}
	if noCloud {
		c = append(c, provider.NoCloud)
	}
	return c
}
