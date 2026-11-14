package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// postScript is a module whose script posts three notes through deck, each waiting for the owner.
func postScript(url string) string {
	return harness.Manifest(fmt.Sprintf(`{"commands": [{"name": "run", "title": "Run", "description": "Posts three notes.",
    "result": {"type": "object"}, "executor": {"type": "script", "script": "main"}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "body": {"text": "{text}"}, "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]}}]},
  "rights": [{"right": "send", "what": ["post"], "why": "posts notes to the service"}]}`, url))
}

const postThree = `
got = [deck("notes.post", {"id": i, "text": "hi " + i})[0] for i in ["a", "b", "c"]]
print(json.dumps({"got": got}))
`

// byWorkRow is an item of deck.asks_list with by_work.
type byWorkRow struct {
	ID      int64
	Kind    string
	What    string
	Waits   string
	Process int64
	Parts   []struct {
		ID      int64
		Title   string
		Text    string
		Actions []map[string]any
	}
	Actions []map[string]any
}

func verbsOf(actions []map[string]any) []string {
	var out []string
	for _, a := range actions {
		out = append(out, fmt.Sprint(a["verb"]))
	}
	return out
}

// TestTheRequestsOfOneWorkAreOneCard: the requests of one process are one item of by_work, a part each with its buttons
// and why it waits, and buttons that answer all at once; each request opens Rights, where it is decided.
func TestTheRequestsOfOneWorkAreOneCard(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t)
	e.Project("notes", postScript(svc.URL))
	e.SaveScript("notes", "scripts/main.py", harness.PythonLib+postThree)
	if p := e.WaitState(e.Start("notes", "run", nil), "done", "error"); !strings.Contains(string(p.Result), "[202,202,202]") {
		t.Fatalf("each post of the script waits for the owner: %+v", p)
	}
	var plain []byWorkRow
	e.Must("deck.asks_list", nil, &plain)
	if len(plain) != 3 || plain[0].Waits != "Waits for your answer: you have not answered for this right yet" {
		t.Fatalf("each request says why it waits: %+v", plain)
	}
	if v := verbsOf(plain[0].Actions); !strings.Contains(strings.Join(v, " "), "rights") || !strings.Contains(strings.Join(v, " "), "allow_always") {
		t.Fatalf("a request opens Rights, where it is decided, and offers always: %v", v)
	}
	var ru []byWorkRow
	e.In("ru").Must("deck.asks_list", nil, &ru)
	if len(ru) != 3 || ru[0].Waits == plain[0].Waits || ru[0].Waits == "" {
		t.Fatalf("why it waits is in the owner's language: %+v", ru)
	}
	byWork := func() []byWorkRow {
		var rows []byWorkRow
		e.Must("deck.asks_list", map[string]any{"by_work": true}, &rows)
		return rows
	}
	rows := byWork()
	if len(rows) != 1 || len(rows[0].Parts) != 3 || rows[0].What != "This work asks for 3 things" || !strings.HasPrefix(strings.Join(verbsOf(rows[0].Actions), " "), "allow_all deny_all") {
		t.Fatalf("the requests of one work are one item with three parts and buttons for all: %+v", rows)
	}
	for _, p := range rows[0].Parts {
		if !strings.Contains(p.Text, "Waits for your answer") || !strings.Contains(strings.Join(verbsOf(p.Actions), " "), "allow") {
			t.Fatalf("each part says why it waits and has its own buttons: %+v", p)
		}
	}
	e.Answer(rows[0].Parts[0].ID, "allow")
	e.Until("the one allowed made", func() bool { return len(svc.Requests()) == 1 })
	if rows = byWork(); len(rows) != 1 || len(rows[0].Parts) != 2 {
		t.Fatalf("a part answered goes, the rest stay: %+v", rows)
	}
	all := rows[0].Actions[0]
	if code, out := e.Call("deck.ask_answer", all["params"].(map[string]any)); code != 200 {
		t.Fatalf("allow all: %d %s", code, out)
	}
	e.Until("all made", func() bool { return len(svc.Requests()) == 3 && len(byWork()) == 0 })

	e.WaitState(e.Start("notes", "run", nil), "done", "error")
	rows = byWork()
	if code, out := e.Call("deck.ask_answer", map[string]any{"ask": rows[0].ID, "answer": "allow_always", "also": []int64{rows[0].Parts[1].ID}}); code != 422 {
		t.Fatalf("always answers a right one at a time: %d %s", code, out)
	}
	if code, out := e.Call("deck.ask_answer", rows[0].Actions[1]["params"].(map[string]any)); code != 200 || len(byWork()) != 0 || len(svc.Requests()) != 3 {
		t.Fatalf("deny all says no to each: %d %s, %d requests", code, out, len(svc.Requests()))
	}
}

// TestAProcessSaysWhyItWaits: the page of a process that waits for the owner's answer to a call says why, by the row
// of the table of decisions, a turn that read from outside saying where.
func TestAProcessSaysWhyItWaits(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}))
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	id := e.Start("notes", "work", nil)
	a := e.WaitAsk()
	var p struct {
		Waits    string `json:"why_waits"`
		WaitsAsk int64  `json:"waits_ask"`
	}
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	if p.Waits != "Waits for your answer: you have not answered for this right yet" || p.WaitsAsk != a.ID {
		t.Fatalf("the process says why it waits and for which request: %+v", p)
	}
	e.Answer(a.ID, "deny")
	e.WaitState(id, "done", "error")
	p.Waits = ""
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	if p.Waits != "" {
		t.Fatalf("a process that ended waits for nothing: %+v", p)
	}
}
