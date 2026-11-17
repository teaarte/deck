package surface

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

// written is what one call of an entry leaves in the base: its lines in the journal (actor tool outcome), the marks
// of the calling process (wrote, read untrusted), the mark of the project and the rows of the feed (kind author).
type written struct {
	Journal []string
	Wrote   bool
	Read    bool
	Taint   bool
	Feed    []string
}

func (w written) String() string {
	return fmt.Sprintf("journal %v, wrote %v, read %v, taint %v, feed %v", w.Journal, w.Wrote, w.Read, w.Taint, w.Feed)
}

// itemsManifest is a module of every executor: an agent, a script that calls deck back, an HTTP tool, a trigger.
func itemsManifest(svc string) string {
	return harness.Declared(harness.Manifest(fmt.Sprintf(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "work", "title": "Work", "description": "Works.", "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Work."}},
    {"name": "main", "title": "Run", "description": "Runs the script.",
     "input": {"type": "object", "properties": {"method": {"type": "string"}, "form": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "main"}},
    {"name": "send", "title": "Send", "description": "Sends a note.",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}}, "executor": {"type": "tool", "tool": "note"}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}],
  "tools": {"http": [{"name": "note", "description": "Sends a note", "method": "POST", "url": "%s/notes",
     "input": {"type": "object", "properties": {"text": {"type": "string"}}}}]},
  "triggers": [{"cron": "0 9 * * *", "command": "work"}]}`, svc)))
}

const upsert = "deck.record_upsert"

func upsertInput(name string) map[string]any {
	return map[string]any{"project": "items", "collection": "items", "records": harness.Jsonl(map[string]any{"name": name})}
}

// TestWhatEachEntryWrites is the table of the eight entries of a call and what one call through each writes: it
// holds deck's present behaviour, so a step that changes what an entry writes names the change here.
func TestWhatEachEntryWrites(t *testing.T) {
	cases := []struct {
		entry string
		call  func(e *harness.Env, agent *harness.Fake) int64
		want  written
	}{
		{"owner over HTTP", func(e *harness.Env, _ *harness.Fake) int64 {
			e.Must(upsert, upsertInput("a"), nil)
			return 0
		}, written{}},
		{"token over HTTP", func(e *harness.Env, _ *harness.Fake) int64 {
			_, tok := e.Token("auto", "deck.*")
			e.As(tok).Must(upsert, upsertInput("a"), nil)
			return 0
		}, written{Journal: []string{"token:1 deck.record_upsert done"}, Taint: true}},
		{"token over MCP", func(e *harness.Env, _ *harness.Fake) int64 {
			_, tok := e.Token("auto", "deck.*")
			s, err := e.Terminal(tok)
			if err != nil {
				e.T.Fatal(err)
			}
			if out, failed := harness.Use(s, "record_upsert", upsertInput("a")); failed {
				e.T.Fatal(out)
			}
			return 0
		}, written{Journal: []string{"token:1 deck.record_upsert done"}, Taint: true}},
		{"turn of an agent over MCP", func(e *harness.Env, agent *harness.Fake) int64 {
			agent.Script = func(ctx context.Context, c *harness.Call) provider.Result {
				// The turn read a page before, as after a fetch.
				e.SQL(`UPDATE process SET read_untrusted = 'a page' WHERE command = 'work' AND state = 'running'`)
				if out, failed := c.Tool(ctx, "record_upsert", upsertInput("a")); failed {
					e.T.Error(out)
				}
				return provider.Result{Structured: json.RawMessage(`{}`)}
			}
			return e.WaitState(e.Start("items", "work", nil), "done", "error").ID
		}, written{Journal: []string{"agent:1 deck.record_upsert done"}, Wrote: true, Read: true, Taint: true}},
		{"script over HTTP", func(e *harness.Env, _ *harness.Fake) int64 {
			e.SaveScript("items", "scripts/main.py", harness.DeckCallScript)
			raw, _ := json.Marshal(upsertInput("a"))
			return e.WaitState(e.Start("items", "main", map[string]any{"method": upsert, "form": string(raw)}), "done", "error").ID
		}, written{Journal: []string{"script:1 deck.record_upsert done"}, Wrote: true}},
		{"trigger", func(e *harness.Env, agent *harness.Fake) int64 {
			e.Clock.Add(25 * time.Hour)
			e.Tick()
			var id int64
			e.Until("the trigger starts work", func() bool {
				_ = e.SQLRow(`SELECT coalesce(max(id), 0) FROM process WHERE command = 'work'`).Scan(&id)
				return id != 0
			})
			return e.WaitState(id, "done", "error").ID
		}, written{Journal: []string{"trigger items.work done"}}},
		{"process of a tool", func(e *harness.Env, _ *harness.Fake) int64 {
			e.Grant("items", "send", "note")
			return e.WaitState(e.Start("items", "send", map[string]any{"text": "hi"}), "done", "error").ID
		}, written{Journal: []string{"tool:1 items.note done"}, Wrote: true, Read: true, Feed: []string{"change tool:1"}}},
		{"owner's answer to an ask", func(e *harness.Env, agent *harness.Fake) int64 {
			agent.Script = func(ctx context.Context, c *harness.Call) provider.Result {
				c.Tool(ctx, "items__send", map[string]any{"text": "hi"})
				return provider.Result{Structured: json.RawMessage(`{}`)}
			}
			id := e.Start("items", "work", nil)
			e.Answer(e.WaitAsk().ID, "allow")
			return e.WaitState(id, "done", "error").ID
		}, written{Journal: []string{"tool:2 items.note waiting", "agent:2 items.note done", "agent:1 items.send done"}, Wrote: true}},
	}
	for _, c := range cases {
		t.Run(c.entry, func(t *testing.T) {
			t.Parallel()
			svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { _, _ = w.Write([]byte(`{"id": "n1"}`)) })
			agent := &harness.Fake{}
			e := harness.New(t, harness.WithProvider(agent), harness.WithClock())
			e.Project("items", itemsManifest(svc.URL))
			journal, feed := count(e, "tool_call"), count(e, "feed")
			id := c.call(e, agent)
			got := writtenSince(e, id, journal, feed)
			if got.String() != c.want.String() {
				t.Errorf("%s writes\n%s\nthe table says\n%s", c.entry, got, c.want)
			}
		})
	}
}

func count(e *harness.Env, table string) int64 {
	var n int64
	if err := e.SQLRow(`SELECT coalesce(max(id), 0) FROM ` + table).Scan(&n); err != nil {
		e.T.Fatal(err)
	}
	return n
}

func writtenSince(e *harness.Env, process, journal, feed int64) written {
	var w written
	rows := e.SQLRows(`SELECT actor, tool, outcome FROM tool_call WHERE id > ? ORDER BY id`, journal)
	for rows.Next() {
		var a, t, o string
		if err := rows.Scan(&a, &t, &o); err != nil {
			e.T.Fatal(err)
		}
		w.Journal = append(w.Journal, strings.Join([]string{a, t, o}, " "))
	}
	_ = rows.Close()
	rows = e.SQLRows(`SELECT kind, author FROM feed WHERE id > ? ORDER BY id`, feed)
	for rows.Next() {
		var k, a string
		if err := rows.Scan(&k, &a); err != nil {
			e.T.Fatal(err)
		}
		w.Feed = append(w.Feed, k+" "+a)
	}
	_ = rows.Close()
	if process != 0 {
		var read string
		if err := e.SQLRow(`SELECT wrote, read_untrusted FROM process WHERE id = ?`, process).Scan(&w.Wrote, &read); err != nil {
			e.T.Fatal(err)
		}
		w.Read = read != ""
	}
	var taint string
	if err := e.SQLRow(`SELECT untrusted FROM project WHERE name = 'items'`).Scan(&taint); err != nil {
		e.T.Fatal(err)
	}
	w.Taint = taint != ""
	return w
}
