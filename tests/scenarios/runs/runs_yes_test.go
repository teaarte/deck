package runs

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// recordSite is site whose work is of a record, so another call for the record joins the work going.
var recordSite = harness.Manifest(`{"writes": ["prompt.md", "scripts/"],
  "rights": [{"right": "network", "what": ["127.0.0.1"], "why": "reads the pages it builds from"}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}],
  "commands": [` + harness.AgentCommand("build", `{"type": "object", "properties": {"record": {"type": "integer"}}}`) + `],
  "screens": [{"name": "main", "title": "Site", "blocks": [
    {"type": "actions", "title": "Build", "actions": [{"label": "Build", "command": "build"}]}]}]}`)

// signedPress is the field deck signs on the button of site's screen, which the owner's press sends.
func signedPress(e *harness.Env) string {
	e.T.Helper()
	return signedOf(e, "site")
}

// signedOf is the field deck signs on the button of the screen of project.
func signedOf(e *harness.Env, project string) string {
	e.T.Helper()
	var blocks struct {
		B0 struct{ Value []map[string]any }
	}
	b, _ := json.Marshal(e.Screen(project, "main", nil))
	if err := json.Unmarshal(b, &blocks); err != nil || len(blocks.B0.Value) != 1 {
		e.T.Fatalf("the button of site's screen: %s", b)
	}
	signed, _ := blocks.B0.Value[0]["params"].(map[string]any)["allows"].(string)
	if signed == "" {
		e.T.Fatalf("the button of site's screen is signed: %s", b)
	}
	return signed
}

// ownSaves are a marked turn's saves of its own module's prompt and script.
func ownSaves(ctx context.Context, c *harness.Call, page string) []string {
	c.Tool(ctx, "web_fetch", map[string]any{"url": page})
	var out []string
	for _, in := range []map[string]any{
		{"name": "prompt.md", "kind": "prompt", "text": "Build it as the page said."},
		{"name": "scripts/main.py", "kind": "script", "text": "print('{}')\n"},
	} {
		got, _ := c.Tool(ctx, "document_save", in)
		out = append(out, got)
	}
	return out
}

// TestATokenThatJoinsAPressedWorkTakesItsYes: a token's call for the record of the work the owner pressed joins it,
// and from then on the work's changes of its module ask.
func TestATokenThatJoinsAPressedWorkTakesItsYes(t *testing.T) {
	t.Parallel()
	page := injected(t, "https://example.com")
	hold := make(chan struct{})
	var mu sync.Mutex
	var got []string
	var runs atomic.Int32
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "build" || runs.Add(1) > 1 {
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		select {
		case <-hold:
		case <-ctx.Done():
		}
		saves := ownSaves(ctx, c, page.URL+"/a")
		mu.Lock()
		got = saves
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))
	e.Project("site", recordSite)
	_, tok := e.Token("board", "site.build")
	var run struct{ Process int64 }
	e.Must("site.build", map[string]any{"record": 1, "allows": signedPress(e)}, &run)
	if code, out := e.As(tok).Call("site.build", map[string]any{"record": 1}); code != 200 || !strings.Contains(string(out), harness.Itoa(run.Process)) {
		t.Fatalf("the token's call joins the work going: %d %s", code, out)
	}
	close(hold)
	e.Until("the work's saves", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return got != nil
	})
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(got[0], `"waiting"`) || !strings.Contains(got[1], `"waiting"`) {
		t.Fatalf("a work another caller joined asks for each change of its module: %q", got)
	}
}

// TestAMarkedTurnThatJoinsTheMastersWorkTakesItsYes: the master's turn that read text from outside and joins the work
// the owner's word started for site takes its yes away.
func TestAMarkedTurnThatJoinsTheMastersWorkTakesItsYes(t *testing.T) {
	t.Parallel()
	page := injected(t, "https://example.com")
	var topic atomic.Int64
	saved, joined := make(chan string, 1), make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch c.Command() {
		case "read":
			return readPage(ctx, c, page.URL+"/a")
		case "work":
			select {
			case <-joined:
			case <-ctx.Done():
			}
			out, _ := c.Tool(ctx, "document_save", map[string]any{"project": "site", "name": "prompt.md", "kind": "prompt", "text": "Build it."})
			saved <- out
			return provider.Result{Text: "done"}
		}
		// A reply of the master; a resumed one names no command nor its input.
		var in struct{ Topic int64 }
		if m := harness.InputOf.FindStringSubmatch(c.Req.Prompt); m != nil && json.Unmarshal([]byte(m[1]), &in) == nil && in.Topic != 0 {
			topic.Store(in.Topic)
		}
		work := map[string]any{"task": "Rebuild site.", "topic": topic.Load()}
		if strings.Contains(c.Req.Prompt, "rebuild site") {
			work["project"] = "site"
		} else {
			c.Tool(ctx, "records_list", map[string]any{"project": "pages", "collection": "ideas"})
		}
		if out, bad := c.Tool(ctx, "master__work", work); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "on it"}
	}))
	e.Project("pages", pagesManifest)
	e.Project("site", siteManifest)
	e.WaitState(e.Start("pages", "read", nil), "done")
	id := e.Say(0, "rebuild site")
	e.MasterSaid(id, 1)
	var allows string
	// The yes a work holds, which deck shows nowhere.
	_ = e.SQLRow(`SELECT allows FROM process WHERE command = 'work'`).Scan(&allows)
	if !strings.Contains(allows, "instructs") {
		t.Fatalf("the work the owner's word started has the yes to site: %s", allows)
	}
	e.Say(id, "and the header after the pages")
	e.MasterSaid(id, 2)
	var works int
	// The works of the master, whose ids the test does not hold.
	_ = e.SQLRow(`SELECT count(*) FROM process WHERE command = 'work'`).Scan(&works)
	if works != 1 {
		t.Fatalf("the marked turn joins the work going: %d works", works)
	}
	close(joined)
	select {
	case s := <-saved:
		if !strings.Contains(s, `"waiting"`) {
			t.Fatalf("a marked turn that joins the work takes its yes: %q", s)
		}
	case <-time.After(harness.Within(time.Minute)):
		t.Fatal("the work saves after the turn joins it")
	}
}

// applyAgain applies the proposal of its record when told to go, and only waits otherwise.
var applyAgain = harness.Manifest(`{"collections": [{"name": "proposals", "description": "A proposal of models.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "project": {"type": "string"}, "models": {"type": "string"}},
      "required": ["name"]}}],
  "commands": [{"name": "apply", "title": "Apply", "description": "Applies a proposal.",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}, "go": {"type": "boolean"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "apply"}}],
  "scripts": [{"name": "apply", "file": "scripts/apply.py", "runtime": "python"}],
  "screens": [{"name": "main", "title": "Proposals", "blocks": [{"type": "table", "title": "Proposals", "source": {"collection": "proposals"},
    "columns": [{"label": "Models", "bind": "models"}],
    "actions": [{"label": "Apply", "command": "apply", "params": {"record": "=_id"},
      "allows": [{"command": "deck.assignment_set", "params": {"role": "text", "project": "=project", "command": "", "models": "=split(models, ',')"}}]}]}]}],
  "menu": ["main"]}`)

const applyAgainScript = `import json, os, sys, time, urllib.request, urllib.error
inp = json.load(sys.stdin)
def call(m, f):
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + m, data=json.dumps(f).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        return json.loads(urllib.request.urlopen(req, timeout=30).read().decode() or "null")
    except urllib.error.HTTPError as e:
        return {"status": e.code, "body": e.read().decode()}
if not inp.get("go"):
    time.sleep(3)
    print("{}")
    sys.exit(0)
d = call("deck.record_get", {"record": inp["record"]})["data"]
print(json.dumps({"call": call("deck.assignment_set", {"role": "text", "project": d["project"], "models": d["models"].split(","), "command": ""})}))
`

// TestTheRunAgainOfAPressedWorkKeepsItsYes: the owner's press for the record of their run going runs it once more
// after it, and that run makes the call the button named without asking, as the first would.
func TestTheRunAgainOfAPressedWorkKeepsItsYes(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "gemma"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	e.Project("films", harness.PlainManifest)
	e.Project("picker", applyAgain)
	e.SaveScript("picker", "scripts/apply.py", applyAgainScript)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/gemma"}}, nil)
	e.Must("deck.record_upsert", map[string]any{"project": "picker", "collection": "proposals",
		"records": harness.Jsonl(map[string]any{"name": "a", "project": "films", "models": "openrouter/qwen"})}, nil)
	params := e.RowAction("picker", "main", "picker.apply", nil)["params"].(map[string]any)
	var run, joined struct{ Process int64 }
	e.Must("picker.apply", map[string]any{"record": params["record"], "allows": params["allows"]}, &run)
	e.Must("picker.apply", map[string]any{"record": params["record"], "go": true, "allows": params["allows"]}, &joined)
	if joined.Process != run.Process {
		t.Fatalf("the press for the record joins the run going: %d, %d", joined.Process, run.Process)
	}
	next := e.Following(run.Process)
	if p := e.WaitState(next, "done", "error"); p.State != "done" || strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("the run again makes the call the owner's press named at once: %+v", p)
	}
	if got := e.Assigned("films", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
		t.Fatalf("the run again applies the models: %v", got)
	}
	var allows string
	// The yes a work holds, which deck shows nowhere.
	_ = e.SQLRow(`SELECT allows FROM process WHERE id = ?`, next).Scan(&allows)
	if !strings.Contains(allows, "instructs") {
		t.Fatalf("the run again keeps the yes of the owner's start: %s", allows)
	}
}

// TestATokenThatJoinsAPressedScriptLeavesItsRunAgainNothing: a token's call for the record of the script the owner
// pressed takes its yes, and the run again on the token's input asks for the call the button named.
func TestATokenThatJoinsAPressedScriptLeavesItsRunAgainNothing(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "gemma"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	e.Project("films", harness.PlainManifest)
	e.Project("picker", applyAgain)
	e.SaveScript("picker", "scripts/apply.py", applyAgainScript)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/gemma"}}, nil)
	e.Must("deck.record_upsert", map[string]any{"project": "picker", "collection": "proposals",
		"records": harness.Jsonl(map[string]any{"name": "a", "project": "films", "models": "openrouter/qwen"})}, nil)
	_, tok := e.Token("board", "picker.apply")
	params := e.RowAction("picker", "main", "picker.apply", nil)["params"].(map[string]any)
	var run struct{ Process int64 }
	e.Must("picker.apply", map[string]any{"record": params["record"], "allows": params["allows"]}, &run)
	if code, out := e.As(tok).Call("picker.apply", map[string]any{"record": params["record"], "go": true}); code != 200 || !strings.Contains(string(out), harness.Itoa(run.Process)) {
		t.Fatalf("the token's call joins the run going: %d %s", code, out)
	}
	var allows string
	// The yes a work holds, which deck shows nowhere.
	_ = e.SQLRow(`SELECT allows FROM process WHERE id = ?`, run.Process).Scan(&allows)
	if strings.Contains(allows, "instructs") {
		t.Fatalf("the token's join takes the yes of the run going: %s", allows)
	}
	next := e.Following(run.Process)
	if p := e.WaitState(next, "done", "error"); !strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("the run again on the token's input asks for the call the owner's press named: %+v", p)
	}
	if got := e.Assigned("films", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("nothing changes without the owner: %v", got)
	}
}

// TestTheWorkHandedOnToTheNextModelKeepsItsYes: the owner's work the first model leaves at its limit goes on on the next
// model, whose marked turn saves its module's prompt and script unasked.
func TestTheWorkHandedOnToTheNextModelKeepsItsYes(t *testing.T) {
	t.Parallel()
	page := injected(t, "https://example.com")
	var mu sync.Mutex
	var got []string
	main := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return harness.Limited(time.Now().Add(time.Hour))
	}}
	backup := &harness.Fake{ProviderName: "backup", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		saves := ownSaves(ctx, c, page.URL+"/a")
		mu.Lock()
		got = saves
		mu.Unlock()
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	e := harness.New(t, harness.WithProvider(main, backup))
	e.Project("site", recordSite)
	e.UseModels("site.build", "fake", "backup")
	var run struct{ Process int64 }
	e.Must("site.build", map[string]any{"allows": signedPress(e)}, &run)
	next := e.Following(run.Process)
	e.WaitState(next, "done", "waiting:answer")
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || strings.Contains(got[0], "waiting") || strings.Contains(got[1], "waiting") {
		t.Fatalf("the next model goes on with the yes of the owner's work: %q", got)
	}
}

// TestAButtonOfNoCallsPressesPastItsWeekOnlyByDecksSignature: a button of site drawn a week ago presses without the
// yes; one drawn now gives it; a field of no calls deck did not sign for site.build is refused.
func TestAButtonOfNoCallsPressesPastItsWeekOnlyByDecksSignature(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock(), harness.WithAgent(harness.Ok))
	e.Project("site", recordSite)
	e.Project("other", recordSite)
	old := signedPress(e)
	parts := strings.SplitN(old, ".", 3)
	for _, forged := range []string{"00.1.[]", "00." + parts[1] + ".[]", "[]", parts[0] + ".9999999999.[]", signedOf(e, "other")} {
		if code, out := e.Call("site.build", map[string]any{"allows": forged}); code < 400 || !strings.Contains(string(out), "only with a button deck drew") {
			t.Fatalf("allows %q deck did not sign is refused: %d %s", forged, code, out)
		}
	}
	yes := func(signed string) bool {
		var run struct{ Process int64 }
		e.Must("site.build", map[string]any{"allows": signed, "retry_key": signed}, &run)
		e.WaitState(run.Process, "done")
		var allows string
		// The yes a work holds, which deck shows nowhere.
		_ = e.SQLRow(`SELECT allows FROM process WHERE id = ?`, run.Process).Scan(&allows)
		return strings.Contains(allows, "instructs")
	}
	e.Clock.Add(7 * 24 * time.Hour)
	if yes(old) {
		t.Fatal("a button drawn a week ago presses without the yes")
	}
	if !yes(signedPress(e)) {
		t.Fatal("a button drawn now gives the yes")
	}
}

// TestATokensReworkOfAPressedWorkTakesItsYes: a token sends the work the owner pressed back with a remark; the turn it
// resumes asks for each change of its module.
func TestATokensReworkOfAPressedWorkTakesItsYes(t *testing.T) {
	t.Parallel()
	page := injected(t, "https://example.com")
	saved := make(chan []string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "back for rework") {
			saved <- ownSaves(ctx, c, page.URL+"/a")
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))
	e.Project("site", recordSite)
	_, tok := e.Token("board", "deck.process_rework")
	var run struct{ Process int64 }
	e.Must("site.build", map[string]any{"record": 1, "allows": signedPress(e)}, &run)
	e.WaitState(run.Process, "done")
	if code, out := e.As(tok).Call("deck.process_rework", map[string]any{"process": run.Process, "remark": "do it as the page says"}); code != 200 {
		t.Fatalf("the token sends the work back: %d %s", code, out)
	}
	select {
	case got := <-saved:
		if !strings.Contains(got[0], `"waiting"`) || !strings.Contains(got[1], `"waiting"`) {
			t.Fatalf("a work another caller sent back asks for each change of its module: %q", got)
		}
	case <-time.After(harness.Within(time.Minute)):
		t.Fatal("the reworked turn saves")
	}
}
