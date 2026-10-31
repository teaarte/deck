package harness

import (
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

func BrowserManifest(domains string) string {
	return `{"version": 13,
  "commands": [{"name": "browse", "title": "Browse", "description": "Browses.", "class": "internal",
    "input": {"type": "object", "properties": {"steps": {"type": "string"}}},
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Browse."}}],
  "domains": ` + domains + `}`
}

type Step struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// Steps is the input of the browse command: the gateway tools the fake agent calls in order.
func Steps(s ...Step) map[string]any {
	b, _ := json.Marshal(s)
	return map[string]any{"steps": string(b)}
}

// SeqAgent calls the steps of its input on a fresh start and keeps every answer; files names files it
// writes into its results folder first.
type SeqAgent struct {
	mu    sync.Mutex
	got   []ToolCall
	Tools []string
	Files []string
	Links map[string]string
}

func (s *SeqAgent) Script(ctx context.Context, c *Call) provider.Result {
	m := InputOf.FindStringSubmatch(c.Req.Prompt)
	if m == nil {
		return provider.Result{Structured: json.RawMessage(`{"resumed": true}`)}
	}
	var in struct{ Steps string }
	_ = json.Unmarshal([]byte(m[1]), &in)
	var list []Step
	_ = json.Unmarshal([]byte(in.Steps), &list)
	for _, f := range s.Files {
		_ = os.WriteFile(filepath.Join(c.Req.Cwd, "..", "results", f), []byte("file"), 0o600)
	}
	for name, target := range s.Links {
		_ = os.Symlink(target, filepath.Join(c.Req.Cwd, "..", "results", name))
	}
	s.mu.Lock()
	s.Tools = c.Req.AllowedTools
	s.mu.Unlock()
	for _, st := range list {
		out, isErr := c.Tool(ctx, st.Tool, st.Args)
		s.mu.Lock()
		s.got = append(s.got, ToolCall{out, isErr})
		s.mu.Unlock()
	}
	return provider.Result{Structured: json.RawMessage(`{}`)}
}

func (s *SeqAgent) Last(n int) []ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ToolCall{}, s.got[len(s.got)-n:]...)
}

func Nav(url string) Step { return Step{"browser_navigate", map[string]any{"url": url}} }

func Click(ref string) Step { return Step{"browser_click", map[string]any{"target": ref}} }
