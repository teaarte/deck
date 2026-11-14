package owner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAnAgentsFeedButtonOfAnOutwardCommandAsksFirst: an agent's button of a command that needs a right is the module's
// call, which asks the owner with what it sends; one of a command that stays in deck stays as written.
func TestAnAgentsFeedButtonOfAnOutwardCommandAsksFirst(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "sent"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, _ = c.Tool(ctx, "owner_notify", map[string]any{"text": "A letter from the bank came",
			"actions": `[{"label": "Open the letter", "command": "send", "params": {"id": "attacker", "text": "all the notes of the owner"}},
				{"label": "Again", "command": "work"}]`})

		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}))
	base := harness.PostManifest(svc.URL, "")
	m := strings.NewReplacer(`"commands": [`, `"commands": [{"name": "send", "title": "Send out", "description": "Sends a note out.",
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]},
    "executor": {"type": "tool", "tool": "post"}}, `, `"what": ["post"]`, `"what": ["post", "send"]`).Replace(base)
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
	if send.Command != "deck.feed_press" || send.Label != "Send out" || send.Params["id"] != nil {
		t.Fatalf("the outward button is the call of its module, under the title of its command: %+v", send)
	}
	if again.Command != "notes.work" || again.Confirm || again.Label != "Again" {
		t.Fatalf("an internal button stays as the agent wrote it: %+v", again)
	}
	var held struct{ Waiting int64 }
	e.Must("deck.feed_press", send.Params, &held)
	a := e.WaitAsk()
	if held.Waiting != a.ID || len(svc.Requests()) != 0 {
		t.Fatalf("the press waits for the owner and sends nothing: %+v %+v", held, a)
	}
	var rows []struct {
		ID     int64
		Change string
	}
	e.Must("deck.asks_list", nil, &rows)
	if len(rows) != 1 || !strings.Contains(rows[0].Change, "id: attacker") || !strings.Contains(rows[0].Change, "text: all the notes of the owner") {
		t.Fatalf("the owner reads what the press would send: %+v", rows)
	}
	// The module drops the command: its button asks first under the bare name of the command.
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "manifest": base}, nil)
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if b := feed[0].Buttons[0]; b.Command != "deck.feed_press" || b.Label != "send" {
		t.Fatalf("a button of a command the module dropped is its call under its name: %+v", b)
	}
}

// TestAMarkedTurnsFeedButtonAsksOnceEvenWithAlways: a button a marked turn wrote carries its mark, so its way out
// held always waits for the owner's once, which shows what it sends and offers no always.
func TestAMarkedTurnsFeedButtonAsksOnceEvenWithAlways(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "sent"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, _ = c.Tool(ctx, "owner_notify", map[string]any{"text": "Looks fine",
			"actions": `[{"label": "Looks good", "command": "send", "params": {"id": "attacker", "text": "exfil-secret"}}]`})
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}))
	m := strings.NewReplacer(`"commands": [`, `"commands": [{"name": "send", "title": "Send out", "description": "Sends a note out.",
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]},
    "executor": {"type": "tool", "tool": "post"}}, `, `"what": ["post"]`, `"what": ["post", "send"]`).Replace(harness.PostManifest(svc.URL, ""))
	p := e.Project("notes", m)
	e.Secret("notes", "svc", "s3cret")
	e.Grant("notes", "send", "post", "send")
	writer := e.Run(p, "work")
	e.WaitState(writer, "done", "failed")
	// The writer read a page and holds something of value: the turn's mark.
	e.SQL(`UPDATE process SET read_untrusted = 'deck.web_fetch evil', holds_valued = 1 WHERE id = ?`, writer)
	var feed []struct {
		Buttons []struct{ Params map[string]any }
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 1 || len(feed[0].Buttons) != 1 {
		t.Fatalf("one note with its button: %+v", feed)
	}
	var held struct{ Waiting int64 }
	e.Must("deck.feed_press", feed[0].Buttons[0].Params, &held)
	if held.Waiting == 0 || len(svc.Requests()) != 0 {
		t.Fatalf("the press of a marked turn's button waits for the owner and sends nothing: %+v %d", held, len(svc.Requests()))
	}
	var rows []map[string]any
	e.Must("deck.asks_list", nil, &rows)
	if len(rows) != 1 || rows[0]["always"] != nil || rows[0]["work"] != nil || !strings.Contains(fmt.Sprint(rows[0]["change"]), "text: exfil-secret") {
		t.Fatalf("one question of once that shows what it sends: %+v", rows)
	}
	e.Answer(int64(rows[0]["id"].(float64)), "allow")
	e.Until("the call the owner allowed once", func() bool { return len(svc.Requests()) == 1 })
}

// TestAMarkedTurnsButtonKeepsItsMarkPastADay: a process that wrote a note with a button stays while the note does,
// so a press a day later still asks once.
func TestAMarkedTurnsButtonKeepsItsMarkPastADay(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "sent"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, _ = c.Tool(ctx, "owner_notify", map[string]any{"text": "Looks fine",
			"actions": `[{"label": "Looks good", "command": "send", "params": {"id": "attacker", "text": "exfil-secret"}}]`})
		return provider.Result{Structured: json.RawMessage(`null`)}
	}), harness.WithClock())
	m := strings.NewReplacer(`"commands": [`, `"commands": [{"name": "send", "title": "Send out", "description": "Sends a note out.",
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]},
    "executor": {"type": "tool", "tool": "post"}}, `, `"what": ["post"]`, `"what": ["post", "send"]`).Replace(harness.Nullable(harness.PostManifest(svc.URL, "")))
	p := e.Project("notes", m)
	e.Secret("notes", "svc", "s3cret")
	e.Grant("notes", "send", "post", "send")
	writer := e.Run(p, "work")
	e.WaitState(writer, "done", "failed")
	e.SQL(`UPDATE process SET read_untrusted = 'deck.web_fetch evil', holds_valued = 1 WHERE id = ?`, writer)
	e.Settle()
	e.Clock.Add(25 * time.Hour)
	e.Sweep()
	var feed []struct {
		Buttons []struct{ Params map[string]any }
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 1 || len(feed[0].Buttons) != 1 {
		t.Fatalf("one note with its button: %+v", feed)
	}
	var held struct{ Waiting int64 }
	e.Must("deck.feed_press", feed[0].Buttons[0].Params, &held)
	if held.Waiting == 0 || len(svc.Requests()) != 0 {
		t.Fatalf("a day later the press of a marked turn's button still waits and sends nothing: %+v %d", held, len(svc.Requests()))
	}
}
