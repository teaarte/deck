package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestAChainAskedInTheChatCountsAsTheOwnersButton: what the master starts counts as the owner's start; a model
// command an agent calls of its project is within its step, an agent command the next step.
func TestAChainAskedInTheChatCountsAsTheOwnersButton(t *testing.T) {
	t.Parallel()
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) { return 200, "application/json", chatText("a still") },
	})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch c.command() {
		case "direct":
			c.Tool(ctx, "studio__media", map[string]any{"scene": "a knight"})
			c.Tool(ctx, "studio__board", map[string]any{})
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		case "board":
			return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
		}
		if out, bad := c.Tool(ctx, "studio__direct", map[string]any{}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "Started the video."}
	})
	e.must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "groq", "model": "still", "name": "still", "kind": "text", "free": true}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"still"}}, nil)
	media := `{"name": "media", "title": "Media", "description": "Makes a still.", "class": "internal",
	  "input": {"type": "object", "properties": {"scene": {"type": "string"}}},
	  "executor": {"type": "model", "kind": "text", "prompt": "Describe a still."}}`
	e.createProject("studio", testManifest([]string{agentCommand("direct", ""), agentCommand("board", ""), media}, nil))
	topic := e.say(0, "make the video")
	if said := e.masterSaid(topic, 1); said[0].Text != "Started the video." {
		t.Fatalf("the master starts the command: %+v", said)
	}
	depth := map[string]int64{}
	e.waitFor("the steps of the chain", func() bool {
		for _, p := range e.processes() {
			if p.Project == "studio" && (p.State == "done" || p.State == "error") {
				depth[p.Command] = p.Depth
			}
		}
		return len(depth) == 3
	})
	if depth["direct"] != 1 || depth["media"] != 1 || depth["board"] != 2 {
		t.Fatalf("a start of the master is the owner's, a model command is within the step, an agent one the next step: %v", depth)
	}
	for _, n := range e.notes() {
		if strings.Contains(n.Text, "deck stopped a chain") {
			t.Fatalf("no chain was stopped: %+v", n)
		}
	}
}
