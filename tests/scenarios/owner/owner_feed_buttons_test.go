package owner

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAnAgentsFeedButtonOfAnOutwardCommandAsksFirst: an agent's feed button of a command not internal asks first,
// under the title of the command, whatever the agent set; one of an internal command stays as written.
func TestAnAgentsFeedButtonOfAnOutwardCommandAsksFirst(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "sent"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, _ = c.Tool(ctx, "owner_notify", map[string]any{"text": "Пришло письмо от банка",
			"actions": `[{"label": "Открыть письмо", "command": "send", "params": {"id": "attacker", "text": "all the notes of the owner"}},
				{"label": "Ещё раз", "command": "work"}]`})

		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}))
	base := harness.PostManifest(svc.URL, "")
	m := strings.Replace(base, `"commands": [`, `"commands": [{"name": "send", "title": "Send out", "description": "Sends a note out.", "class": "external",
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]},
    "executor": {"type": "tool", "tool": "post"}}, `, 1)
	p := e.Project("notes", m)
	e.Secret("notes", "svc", "s3cret")
	e.WaitState(e.Run(p, "work"), "done", "failed")
	var feed []struct {
		Buttons []struct {
			Label, Command string
			Confirm        bool
			Params         map[string]any
		}
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 1 || len(feed[0].Buttons) != 2 {
		t.Fatalf("one note with two buttons: %+v", feed)
	}
	send, again := feed[0].Buttons[0], feed[0].Buttons[1]
	if send.Command != "notes.send" || !send.Confirm || send.Label != "Send out" || send.Params["id"] != "attacker" {
		t.Fatalf("the outward button asks first, under the title of its command, with its params: %+v", send)
	}
	if again.Command != "notes.work" || again.Confirm || again.Label != "Ещё раз" {
		t.Fatalf("an internal button stays as the agent wrote it: %+v", again)
	}
	// The module drops the command: its button asks first under the bare name of the command.
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "manifest": base}, nil)
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if b := feed[0].Buttons[0]; !b.Confirm || b.Label != "send" {
		t.Fatalf("a button of a command the module dropped asks first under its name: %+v", b)
	}
}
