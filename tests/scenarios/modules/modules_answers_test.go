package modules

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// exampleOf is the input a tool's description gives as its example; nil when it gives none.
func exampleOf(desc string) map[string]any {
	_, rest, ok := strings.Cut(desc, "Example input: ")
	if !ok {
		return nil
	}
	var ex map[string]any
	if json.NewDecoder(strings.NewReader(rest)).Decode(&ex) != nil {
		return nil
	}
	return ex
}

// TestAWeakAgentCallsRecordsRightByTheirTools: a small model that knows only what the tools say lists, reads and edits
// a record with no call wrong; untold, it takes data for a JSON text and edits by key the changed field alone.
func TestAWeakAgentCallsRecordsRightByTheirTools(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var wrong []string
	miss := func(what string) {
		mu.Lock()
		defer mu.Unlock()
		wrong = append(wrong, what)
	}
	weak := func(ctx context.Context, c *harness.Call) provider.Result {
		res, err := c.Session.ListTools(ctx, &mcp.ListToolsParams{})
		if err != nil {
			miss("tools/list: " + err.Error())
			return provider.Result{}
		}
		says := map[string]string{}
		for _, tl := range res.Tools {
			says[tl.Name] = tl.Description
		}
		// data reads the fields of a record as the tool says they come.
		data := func(tool string, raw json.RawMessage) map[string]any {
			var fields map[string]any
			if !strings.Contains(says[tool], `"data": object`) {
				var text string
				if err := json.Unmarshal(raw, &text); err != nil {
					miss(tool + ": data read as a JSON text: " + err.Error())
					return nil
				}
				raw = json.RawMessage(text)
			}
			_ = json.Unmarshal(raw, &fields)
			return fields
		}
		out, bad := c.Tool(ctx, "records_list", map[string]any{"collection": "ideas"})
		var list []struct {
			ID   int64
			Data json.RawMessage
		}
		if bad || json.Unmarshal([]byte(out), &list) != nil || len(list) != 2 {
			miss("records_list: " + out)
			return provider.Result{}
		}
		id := list[0].ID
		if data("records_list", list[0].Data) == nil {
			return provider.Result{}
		}
		out, bad = c.Tool(ctx, "record_get", map[string]any{"record": id})
		var rec struct{ Data json.RawMessage }
		if bad || json.Unmarshal([]byte(out), &rec) != nil {
			miss("record_get: " + out)
			return provider.Result{}
		}
		fields := data("record_get", rec.Data)
		if fields == nil {
			return provider.Result{}
		}
		in := map[string]any{"collection": "ideas", "key": fields["title"], "data": map[string]any{"score": 9}}
		if ex := exampleOf(says["record_update"]); ex != nil {
			in = map[string]any{}
			for k, v := range ex {
				switch v.(type) {
				case map[string]any:
					whole := map[string]any{}
					for f, x := range fields {
						whole[f] = x
					}
					whole["score"] = 9
					in[k] = whole
				case float64:
					in[k] = id
				}
			}
		}
		if out, bad := c.Tool(ctx, "record_update", in); bad {
			miss("record_update: " + out)
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 1}`)}
	}
	e := harness.New(t, harness.WithAgent(weak))
	p := e.Project("ideas", harness.IdeasManifest)
	e.Must("deck.record_upsert", map[string]any{"project": "ideas", "collection": "ideas", "records": harness.Jsonl(
		map[string]any{"title": "alpha", "score": 1}, map[string]any{"title": "beta", "score": 2})}, nil)
	e.WaitState(e.Run(p, "collect"), "done")
	mu.Lock()
	defer mu.Unlock()
	if len(wrong) > 0 {
		t.Fatalf("a weak agent called by what the tools say and went wrong: %q", wrong)
	}
	var recs []struct {
		Data struct {
			Title string
			Score int
		}
	}
	e.Must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas"}, &recs)
	if len(recs) != 2 || recs[0].Data.Title != "alpha" || recs[0].Data.Score != 9 {
		t.Fatalf("the agent edits the record and keeps its key: %+v", recs)
	}
}

// TestTheToolOfACommandShowsItsResultAndAnExample: the tool of a module's command tells an agent its result schema
// without descriptions, only fields and types when it is long, and an input of examples only when they fill it.
func TestTheToolOfACommandShowsItsResultAndAnExample(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	says := map[string]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		res, err := c.Session.ListTools(ctx, &mcp.ListToolsParams{})
		if err == nil {
			mu.Lock()
			defer mu.Unlock()
			for _, tl := range res.Tools {
				says[tl.Name] = tl.Description
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))
	long := map[string]any{}
	for _, f := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa"} {
		long[f] = map[string]any{"type": "string", "description": "The " + f + " of the report, as the owner reads it."}
		long[f+"_n"] = map[string]any{"type": []string{"integer", "null"}, "minimum": 0}
	}
	report, _ := json.Marshal(map[string]any{"name": "report", "title": "Report", "description": "Reports.", "class": "internal",
		"input":    map[string]any{"type": "object", "properties": map[string]any{"day": map[string]any{"type": "string", "examples": []string{"2026-11-11"}}, "n": map[string]any{"type": "integer"}}, "required": []string{"day", "n"}},
		"result":   map[string]any{"type": "object", "properties": long},
		"executor": map[string]any{"type": "agent", "prompt": "Report."}})
	lookup := `{"name": "lookup", "title": "Lookup", "description": "Finds a note.", "class": "internal",
	  "input": {"type": "object", "properties": {"topic": {"type": "string", "examples": ["tea", "coffee"]}, "limit": {"type": "integer"}}, "required": ["topic"]},
	  "result": {"type": "object", "title": "Notes", "properties": {"description": {"type": "string", "description": "What was found"}}},
	  "executor": {"type": "agent", "prompt": "Find notes."}}`
	review := `{"name": "review", "title": "Review", "description": "Reviews notes.", "class": "internal",
	  "result": {"type": "object", "properties": {"n": {"type": "integer"}}},
	  "executor": {"type": "agent", "prompt": "Review notes.", "calls": ["notes.lookup", "notes.report"]}}`
	p := e.Project("notes", harness.TestManifest([]string{lookup, string(report), review}, nil))
	e.WaitState(e.Run(p, "review"), "done")
	mu.Lock()
	defer mu.Unlock()
	if want := `Finds a note. Result of its process: {"properties":{"description":{"type":"string"}},"type":"object"}. Example input: {"topic": "tea"}.`; says["notes__lookup"] != want {
		t.Fatalf("the tool gives the result without descriptions and the example:\n%s\nwant\n%s", says["notes__lookup"], want)
	}
	rep := says["notes__report"]
	if !strings.Contains(rep, `Result of its process: {"alpha": string, "alpha_n": integer|null, "beta": string,`) ||
		strings.Contains(rep, "as the owner reads it") || strings.Contains(rep, "Example input") {
		t.Fatalf("a long result gives fields and types, an example missing a required field none: %q", rep)
	}
}

// TestAnAgentIsWarnedOfACallWithoutAnAnswer: manifest_check and an import warn of an agent command whose calls name a
// command of the module without a result schema once, not one no agent may call, and still take the module.
func TestAnAgentIsWarnedOfACallWithoutAnAnswer(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	module := func(result string) string {
		return harness.Manifest(`{"commands": [
	  {"name": "lookup", "title": "Lookup", "description": "Finds a note.", "class": "internal"` + result + `,
	   "executor": {"type": "agent", "prompt": "Find notes."}},
	  {"name": "tidy", "title": "Tidy", "description": "Tidies notes.", "class": "internal", "callers": ["owner"],
	   "executor": {"type": "agent", "prompt": "Tidy notes."}},
	  {"name": "review", "title": "Review", "description": "Reviews notes.", "class": "internal",
	   "result": {"type": "object", "properties": {"n": {"type": "integer"}}},
	   "executor": {"type": "agent", "prompt": "Review notes.", "calls": ["notes.lookup", "notes.tidy", "draft.lookup", "deck.records_list"]}}]}`)
	}
	shaped := `, "result": {"type": "object", "properties": {"notes": {"type": "array", "items": {"type": "string"}}}}`
	warned := func(ws []string) bool {
		calls := slices.DeleteFunc(slices.Clone(ws), func(w string) bool { return !strings.Contains(w, "command review: its agent calls") })
		return len(calls) == 1 && strings.Contains(calls[0], "calls lookup, which has no result schema")
	}
	for _, c := range []struct {
		name, result string
		warn         bool
	}{{"no result", "", true}, {"a result", shaped, false}} {
		var v struct {
			Ok       bool
			Refused  []string
			Warnings []string
		}
		e.Must("deck.manifest_check", map[string]any{"manifest": module(c.result)}, &v)
		if !v.Ok || warned(v.Warnings) != c.warn {
			t.Fatalf("%s: manifest_check warns %v: %+v", c.name, c.warn, v)
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(module("")), 0o600); err != nil {
		t.Fatal(err)
	}
	var im struct {
		Name     string
		Warnings []string
	}
	e.Must("deck.project_import", map[string]any{"folder": dir, "name": "notes"}, &im)
	if im.Name != "notes" || !warned(im.Warnings) {
		t.Fatalf("an import takes the module and warns: %+v", im)
	}
	var other struct{ Warnings []string }
	e.Must("deck.manifest_check", map[string]any{"project": "notes", "manifest": strings.NewReplacer(`"notes.lookup"`, `"other.lookup"`, `"draft.lookup"`, `"else.lookup"`).Replace(module(""))}, &other)
	if warned(other.Warnings) {
		t.Fatalf("a command of another project is not one of the module: %+v", other.Warnings)
	}
}
