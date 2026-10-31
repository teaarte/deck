package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
)

// DeckCallScript calls the deck command of its input's method with the JSON of form and prints the status and body.
const DeckCallScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + inp["method"], data=inp["form"].encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
try:
    r = urllib.request.urlopen(req, timeout=30)
    status, body = r.status, r.read().decode()
except urllib.error.HTTPError as e:
    status, body = e.code, e.read().decode()
print(json.dumps({"status": status, "body": body}))
`

type Called struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// ScriptCall runs the script command main of project with a call of method and returns what deck answered it.
func (e *Env) ScriptCall(project, method string, form map[string]any) Called {
	e.T.Helper()
	raw, _ := json.Marshal(form)
	p := e.WaitState(e.Start(project, "main", map[string]any{"method": method, "form": string(raw)}), "done", "error")
	var c Called
	if err := json.Unmarshal(p.Result, &c); err != nil {
		e.T.Fatalf("the script answers what deck said: %+v %v", p, err)
	}
	return c
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

func WriteIdeas(ctx context.Context, c *Call, topic string) {
	c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": Jsonl(
		map[string]any{"title": "alpha", "score": 7, "topic": topic}, map[string]any{"title": "beta", "score": 3, "topic": topic},
		map[string]any{"title": "gamma", "score": 9, "topic": topic}, map[string]any{"title": "broken"})})
}

// MasterScript plays the master: the review tells the owner in the feed, on_error says what it got; failing makes
// the work command of the other projects fail.
func MasterScript(ctx context.Context, c *Call) provider.Result {
	switch c.Command() {
	case "review":
		if out, bad := c.Tool(ctx, "notify", map[string]any{"project": "work", "text": "The queue has not moved for three days; say \"unstick it\" to have it done."}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "one note"}
	case "on_error":
		return provider.Result{Text: "looked"}
	case "work":
		return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
	}
	return provider.Result{Text: "ok"}
}

// ApplyScript applies the proposal of its id, or with none every proposal, by deck.assignment_set; with relay it
// starts apply itself, as another caller, with the allows it was given.
const ApplyScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
def call(m, f):
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + m, data=json.dumps(f).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        body = urllib.request.urlopen(req, timeout=30).read().decode()
        return json.loads(body) if body.strip() else None
    except urllib.error.HTTPError as e:
        return {"status": e.code, "body": e.read().decode()}
if inp.get("relay"):
    print(json.dumps(call("picker.apply", {"id": inp["id"], "allows": inp["relay"]})))
    sys.exit(0)
ids = [inp["id"]] if inp.get("id") else [r["id"] for r in call("deck.records_list", {"collection": "proposals"})]
out = []
for i in ids:
    d = call("deck.record_get", {"record": i})["data"]
    out.append(call("deck.assignment_set", {"role": "text", "project": d["project"], "models": d["models"].split(","), "command": d.get("command", "")}))
print(json.dumps({"calls": out}))
`

// PressScript makes the deck calls of its input, one or many, and prints deck's last answer as its result; with n it
// only names ask n as the one its work waits for.
const PressScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
if inp.get("n"):
    print(json.dumps({"waiting": inp["n"]}))
    sys.exit(0)
calls = json.loads(inp["form"]) if inp["method"] == "many" else [{"method": inp["method"], "form": json.loads(inp["form"])}]
body = "{}"
for c in calls:
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + c["method"], data=json.dumps(c["form"]).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        body = urllib.request.urlopen(req, timeout=30).read().decode()
    except urllib.error.HTTPError as e:
        body = json.dumps({"status": e.code, "body": e.read().decode()})
print(body if body.strip() else "{}")
`

// WebNotesAgent scores a title into two rows, and on tell notifies the owner with the note page and a button.
func WebNotesAgent() Script {
	return func(ctx context.Context, c *Call) provider.Result {
		switch c.Command() {
		case "score":
			return provider.Result{Structured: json.RawMessage(`[{"title": "alpha", "score": 3}, {"title": "beta", "score": 5}]`)}
		case "mark", "tag":
			var in struct{ Text, Tag string }
			if m := InputOf.FindStringSubmatch(c.Req.Prompt); m != nil {
				_ = json.Unmarshal([]byte(m[1]), &in)
			}
			out, _ := json.Marshal(map[string]string{"seen": in.Text + in.Tag})
			return provider.Result{Structured: out}
		case "tell":
			var recs []struct{ ID int64 }
			out, _ := c.Tool(ctx, "records_list", map[string]any{"collection": "notes"})
			_ = json.Unmarshal([]byte(out), &recs)
			if text, bad := c.Tool(ctx, "notify", map[string]any{"text": "alpha scored high", "screen": "note", "record": recs[0].ID,
				"actions": fmt.Sprintf(`[{"label": "Touch it", "icon": "hand-finger", "confirm": true, "command": "touch", "params": {"note": %d}}]`, recs[0].ID)}); bad {
				return provider.Result{Structured: json.RawMessage(fmt.Sprintf(`{"error": %q}`, text))}
			}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}
}

// WebAgent posts a new note through the external tool on work and asks a question with options on ask.
func WebAgent() Script {
	var n atomic.Int32
	return func(ctx context.Context, c *Call) provider.Result {
		if c.Req.Resume {
			return provider.Result{Structured: json.RawMessage(`{}`)}
		}
		switch c.Command() {
		case "work":
			c.Tool(ctx, "notes__post", map[string]any{"id": fmt.Sprintf("n%d", n.Add(1)), "text": "hi"})
		case "ask":
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which topic?", "options": []map[string]any{{"label": "go"}, {"label": "rust"}}}}})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}
}

// ClipsEnv is deck with the clips module and the fake agent script.
func ClipsEnv(t *testing.T, script Script, opts ...Option) *Env {
	t.Helper()
	e := New(t, append([]Option{WithAgent(script)}, opts...)...)
	e.Project("clips", ClipsModule)
	e.SaveScript("clips", "scripts/render.py", ClipsRender)
	return e
}
