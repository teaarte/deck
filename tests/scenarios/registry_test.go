package scenarios

import (
	"context"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type paramSchema struct {
	Type   string       `json:"type"`
	Format string       `json:"format"`
	Enum   []any        `json:"enum"`
	Items  *paramSchema `json:"items"`
}

var intentFormats = map[string][]string{
	"string":  {"", "text", "date", "date-time", "file", "project"},
	"integer": {"", "record", "card", "project", "document", "process", "ask"},
	"number":  {""},
	"boolean": {""},
}

func intentType(p paramSchema, list bool) bool {
	if p.Type == "array" {
		return list && p.Items != nil && intentType(*p.Items, false)
	}
	formats, ok := intentFormats[p.Type]
	if !ok || len(p.Enum) > 0 && p.Type != "string" {
		return false
	}
	return slices.Contains(formats, p.Format)
}

func TestEveryCommandFitsAppIntents(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("ideas", harness.IdeasManifest)
	if code, _ := e.Do("GET", "/api/commands", nil); code != 404 && code != 405 {
		t.Fatalf("the list of commands is commands_list alone: %d", code)
	}
	cmds := e.Commands()
	names := map[string]bool{}
	for _, c := range cmds {
		names[c.Name] = true
		if c.Title == "" || c.Description == "" || c.Class == "" {
			t.Errorf("%s: title, description and class are required", c.Name)
		}
		var in struct {
			Type       string                 `json:"type"`
			Properties map[string]paramSchema `json:"properties"`
		}
		if err := json.Unmarshal(c.Input, &in); err != nil || in.Type != "object" {
			t.Errorf("%s: input must be an object schema: %s", c.Name, c.Input)
			continue
		}
		for n, p := range in.Properties {
			if !intentType(p, true) {
				t.Errorf("%s.%s: type %q format %q is outside the App Intents subset", c.Name, n, p.Type, p.Format)
			}
		}
	}
	for _, want := range []string{"deck.project_create", "deck.document_save", "deck.records_upsert", "deck.ask_answer", "ideas.collect"} {
		if !names[want] {
			t.Errorf("command %s is missing from the registry", want)
		}
	}
}

func TestCommandsListDeckSelectsBuiltins(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("ideas", harness.IdeasManifest)
	for _, kind := range []string{registry.Owner, registry.Master} {
		act := registry.Actor{Kind: kind, Workspace: 1}
		out, err := e.App.Registry.Invoke(t.Context(), act, "deck.commands_list", json.RawMessage(`{"project":"deck","fields":["name","description","owner_only","form","form_params"]}`))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		var rows []struct {
			Name      string `json:"name"`
			OwnerOnly bool   `json:"owner_only"`
			Form      string `json:"form"`
		}
		if err := json.Unmarshal(raw, &rows); err != nil || len(rows) == 0 {
			t.Fatalf("built-in commands for %s: %s, %v", kind, raw, err)
		}
		found := false
		for _, row := range rows {
			if !strings.HasPrefix(row.Name, "deck.") {
				t.Fatalf("built-in filter includes a module command: %s", row.Name)
			}
			if row.Name == "deck.secret_set" {
				found = true
				if row.OwnerOnly != (kind == registry.Master) || kind == registry.Master && row.Form == "" {
					t.Fatalf("owner-only command keeps its form and access for %s: %+v", kind, row)
				}
			}
		}
		if !found {
			t.Fatal("deck.secret_set is absent")
		}
	}
	var module []struct{ Name string }
	e.Must("deck.commands_list", map[string]any{"project": "ideas"}, &module)
	if len(module) == 0 {
		t.Fatal("module filter is empty")
	}
	for _, row := range module {
		if !strings.HasPrefix(row.Name, "ideas.") {
			t.Fatalf("module filter includes %s", row.Name)
		}
	}
	e.Refused("deck.commands_list", map[string]any{"project": "missing"}, 404, `no project "missing"`)
}

func TestUnknownCommandAndBadInput(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	if code, _ := e.Call("deck.nope", nil); code != 404 {
		t.Fatalf("unknown command: want 404, got %d", code)
	}
	code, out := e.Call("deck.project_create", map[string]any{"name": "x", "manifest": "{}", "color": "red"})
	if code != 422 || !strings.Contains(string(out), "color") {
		t.Fatalf("unknown parameter: want 422, got %d %s", code, out)
	}
	code, out = e.Call("deck.project_create", map[string]any{"name": "Deck", "manifest": harness.IdeasManifest})
	if code != 422 {
		t.Fatalf("bad project name: want 422, got %d %s", code, out)
	}
}

type docView struct {
	ID        int64  `json:"id"`
	OwnerKind string `json:"owner_kind"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision"`
	Text      string `json:"text"`
}

func TestManifestRevisionsAndRevert(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var created struct{ ID int64 }
	e.Must("deck.project_create", map[string]any{"name": "ideas", "manifest": harness.IdeasManifest, "prompt": "# Ideas of the day\nShort ideas for a solo developer."}, &created)
	p := created.ID
	var projects []struct {
		Name, Description string
		Screens           []struct{ Name string }
	}
	e.ProjectsOf(&projects)
	if len(projects) != 1 || projects[0].Description != "Ideas of the day" || len(projects[0].Screens) != 1 {
		t.Fatalf("projects_list: %+v", projects)
	}

	noTrigger := strings.Replace(harness.IdeasManifest, `"triggers": [{"command": "collect", "cron": "0 9 * * *"}]`, `"triggers": []`, 1)
	var saved struct{ Revision int64 }
	e.Must("deck.manifest_save", map[string]any{"project": "ideas", "manifest": noTrigger, "reason": "no schedule"}, &saved)
	if saved.Revision != 2 {
		t.Fatalf("want revision 2, got %d", saved.Revision)
	}
	if ts, _ := e.App.Store.Q.ListProjectTriggers(context.Background(), p); len(ts) != 0 {
		t.Fatalf("removed trigger must be dropped, got %+v", ts)
	}

	code, out := e.Call("deck.manifest_save", map[string]any{"project": "ideas", "manifest": `{"version": 13, "commands": [{"name": "x"}]}`})
	if code != 422 || !strings.Contains(string(out), "title") || !strings.Contains(string(out), "executor") {
		t.Fatalf("invalid manifest: want 422 with reason, got %d %s", code, out)
	}
	var cur struct {
		Document, Revision int64
		Manifest           string
	}
	e.Must("deck.manifest_get", map[string]any{"project": "ideas"}, &cur)
	if cur.Revision != 2 || cur.Manifest != manifest.Canonical(noTrigger) {
		t.Fatalf("rejected manifest must leave revision 2, got %d", cur.Revision)
	}

	var h struct {
		Revisions []struct {
			Revision int64
			Author   string
			Reason   string
			Current  bool
		}
	}
	e.Must("deck.document_get", map[string]any{"document": cur.Document, "history": true}, &h)
	revs := h.Revisions
	if len(revs) != 2 || revs[0].Reason != "no schedule" || revs[0].Author != "owner" || !revs[0].Current {
		t.Fatalf("revisions: %+v", revs)
	}
	e.Must("deck.document_revert", map[string]any{"document": cur.Document, "revision": 1}, &saved)
	e.Must("deck.manifest_get", map[string]any{"project": "ideas"}, &cur)
	if saved.Revision != 3 || cur.Manifest != manifest.Canonical(harness.IdeasManifest) {
		t.Fatalf("revert must bring revision 1 back as revision 3, got %d", saved.Revision)
	}
	if ts, _ := e.App.Store.Q.ListProjectTriggers(context.Background(), p); len(ts) != 1 {
		t.Fatalf("revert must restore the trigger, got %+v", ts)
	}
}

func TestDocumentsWithRevisions(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var d docView
	e.Must("deck.document_save", map[string]any{"name": "notes/rules.md", "kind": "doc", "text": "v1"}, &d)
	e.Must("deck.document_save", map[string]any{"name": "notes/rules.md", "kind": "doc", "text": "v2", "reason": "sharper"}, &d)
	if d.Revision != 2 {
		t.Fatalf("want revision 2, got %+v", d)
	}
	var got docView
	e.Must("deck.document_get", map[string]any{"name": "notes/rules.md"}, &got)
	if got.Text != "v2" {
		t.Fatalf("current text: %+v", got)
	}
	e.Must("deck.document_get", map[string]any{"document": d.ID, "revision": 1}, &got)
	if got.Text != "v1" || got.Revision != 1 {
		t.Fatalf("revision 1: %+v", got)
	}
	e.Must("deck.document_revert", map[string]any{"document": d.ID, "revision": 1}, nil)
	e.Must("deck.document_get", map[string]any{"document": d.ID}, &got)
	if got.Text != "v1" || got.Revision != 3 {
		t.Fatalf("revert: %+v", got)
	}
	var list []docView
	e.Must("deck.documents_list", map[string]any{"kind": "doc"}, &list)
	if len(list) != 1 || list[0].Name != "notes/rules.md" {
		t.Fatalf("documents_list: %+v", list)
	}

	for _, bad := range []map[string]any{
		{"name": "../etc/passwd", "kind": "doc", "text": "x"},
		{"name": "notes/rules.md", "kind": "script", "text": "x"},
		{"name": "skills/writer/SKILL.md", "kind": "skill", "text": "no frontmatter"},
		{"name": "skills/writer/SKILL.md", "kind": "skill", "text": "---\nname: other\ndescription: x\n---\n"},
		{"name": "writer/SKILL.md", "kind": "skill", "text": "---\nname: writer\ndescription: x\n---\n"},
		{"name": "skills/writer/notes.md", "kind": "doc", "text": "x"},
		{"name": "skills/writer/SKILL.md", "kind": "skill", "text": "---\nname: writer\ndescription: x\nallowed-tools: Bash\n---\n"},
		{"name": "skills/writer/SKILL.md", "kind": "skill", "text": "---\nname: writer\ndescription: x\nhooks:\n  PreToolUse: []\n---\n"},
		{"name": "skills/writer/SKILL.md", "kind": "skill", "text": "---\nname: writer\ndescription: x\n\"allowed-tools\": Bash\n---\n"},
		{"name": "skills/writer/SKILL.md", "kind": "skill", "text": "---\nname: writer\ndescription: x\nhooks : {}\n---\n"},
		{"name": "skills/writer/SKILL.md", "kind": "skill", "text": "---\n{name: writer, description: x, allowed_tools: Bash}\n---\n"},
		{"name": "manifest.json", "kind": "manifest", "text": "{}"},
		{"name": "a.md", "kind": "poem", "text": "x"},
	} {
		if code, out := e.Call("deck.document_save", bad); code != 422 {
			t.Errorf("%v: want 422, got %d %s", bad, code, out)
		}
	}
	skill := "---\nname: writer\ndescription: Writes in the owner's voice.\n---\n# Writer\n"
	e.Must("deck.document_save", map[string]any{"name": "skills/writer/SKILL.md", "kind": "skill", "text": skill}, nil)
}

func TestRecordCommands(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("ideas", harness.IdeasManifest)
	var up struct {
		Written int
		Errors  []string
	}
	e.Must("deck.records_upsert", map[string]any{"project": "ideas", "collection": "ideas",
		"records": harness.Jsonl(map[string]any{"title": "alpha", "score": 7}, map[string]any{"title": "beta", "score": 3}) + "not json\n"}, &up)
	if up.Written != 2 || len(up.Errors) != 1 || !strings.Contains(up.Errors[0], "line 3") {
		t.Fatalf("upsert: %+v", up)
	}
	var recs []struct {
		ID    int64
		Key   string
		Title string
		Data  map[string]any
	}
	e.Must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas"}, &recs)
	if len(recs) != 2 || recs[0].Title != "alpha" || recs[0].Key != "alpha" {
		t.Fatalf("records_list: %+v", recs)
	}
	code, out := e.Call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"title": "renamed", "score": 7}`})
	if code != 422 || !strings.Contains(string(out), "key") {
		t.Fatalf("key change must be refused, got %d %s", code, out)
	}
	e.Must("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"title": "alpha", "score": 8}`}, nil)
	e.Must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "limit": 1}, &recs)
	if len(recs) != 1 || recs[0].Title != "beta" {
		t.Fatalf("limit keeps the newest: %+v", recs)
	}
	e.Must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas"}, &recs)
	if recs[0].Data["score"] != float64(8) {
		t.Fatalf("record_update not applied: %+v", recs[0])
	}
	if code, _ := e.Call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"title": "alpha"}`}); code != 422 {
		t.Fatalf("data outside the schema must be refused, got %d", code)
	}
}

func TestProcessCommands(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))

	p := e.Project("ideas", harness.IdeasManifest)
	e.Project("other", harness.IdeasManifest)
	id := e.Run(p, "collect")
	e.WaitState(id, "done")
	var ps []harness.Proc
	e.Must("deck.processes_list", map[string]any{"project": "other"}, &ps)
	if len(ps) != 0 {
		t.Fatalf("filter by project: %+v", ps)
	}
	e.Must("deck.processes_list", map[string]any{"project": "ideas", "state": "done"}, &ps)
	if len(ps) != 1 || ps[0].ID != id {
		t.Fatalf("filter by project and state: %+v", ps)
	}
	if code, _ := e.Call("deck.process_get", map[string]any{"process": 999}); code != 404 {
		t.Fatalf("unknown process: want 404, got %d", code)
	}
}

func TestAgentStaysInsideItsProject(t *testing.T) {
	t.Parallel()
	type seen struct {
		tools           []string
		foreign, skill  string
		artifact, other string
	}
	var s seen
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		res, _ := c.Session.ListTools(ctx, &mcp.ListToolsParams{})
		for _, tl := range res.Tools {
			s.tools = append(s.tools, tl.Name)
		}
		s.foreign, _ = c.Tool(ctx, "records_upsert", map[string]any{"project": "other", "collection": "ideas", "records": harness.Jsonl(map[string]any{"title": "x", "score": 1})})
		s.skill, _ = c.Tool(ctx, "document_save", map[string]any{"name": "x/SKILL.md", "kind": "skill", "text": "---\nname: x\ndescription: y\n---\n"})
		c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": harness.Jsonl(map[string]any{"title": "alpha", "score": 7})})
		recs, _ := c.Tool(ctx, "records_list", map[string]any{"collection": "ideas"})
		var rs []struct{ ID int64 }
		_ = json.Unmarshal([]byte(recs), &rs)
		if len(rs) == 1 {
			s.artifact, _ = c.Tool(ctx, "document_save", map[string]any{"name": "report.md", "kind": "artifact", "text": "done", "record": rs[0].ID})
		}
		s.other, _ = c.Tool(ctx, "manifest_get", map[string]any{"project": "other"})
		return provider.Result{Structured: json.RawMessage(`{"written": 1}`)}
	}))

	p := e.Project("ideas", harness.IdeasManifest)
	e.Project("other", harness.IdeasManifest)
	e.WaitState(e.Run(p, "collect"), "done")

	for _, want := range []string{"records_upsert", "document_save", "ideas__collect", "process_get"} {
		if !slices.Contains(s.tools, want) {
			t.Errorf("agent must see tool %s, got %v", want, s.tools)
		}
	}
	for _, hidden := range []string{"manifest_save", "project_create", "ask_answer", "other__collect"} {
		if slices.Contains(s.tools, hidden) {
			t.Errorf("agent must not see tool %s", hidden)
		}
	}
	if !strings.Contains(s.foreign, "another project") || !strings.Contains(s.other, "another project") {
		t.Fatalf("foreign project must be refused: %q, %q", s.foreign, s.other)
	}
	if !strings.Contains(s.skill, "does not write x/SKILL.md") {
		t.Fatalf("agent writing a skill must be refused: %q", s.skill)
	}
	var d docView
	if err := json.Unmarshal([]byte(s.artifact), &d); err != nil || d.Kind != "artifact" || d.Revision != 1 {
		t.Fatalf("artifact bound to a record: %q", s.artifact)
	}
	var list []struct {
		Name      string
		OwnerKind string `json:"owner_kind"`
	}
	e.Must("deck.documents_list", map[string]any{"project": "ideas", "kind": "artifact"}, &list)
	if len(list) != 1 || list[0].OwnerKind != "record" {
		t.Fatalf("artifact listing: %+v", list)
	}
	var h struct{ Revisions []struct{ Author string } }
	e.Must("deck.document_get", map[string]any{"document": d.ID, "history": true}, &h)
	revs := h.Revisions
	if len(revs) != 1 || !strings.HasPrefix(revs[0].Author, "agent:") {
		t.Fatalf("artifact author: %+v", revs)
	}
}

func TestOutOfRangeIntegersAreRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("nums", harness.CalcManifest)
	var res struct {
		Written int
		Errors  []string
	}
	e.Must("deck.records_upsert", map[string]any{"project": "nums", "collection": "nums",
		"records": "{\"n\": 18446744073709551614}\n{\"n\": 18446744073709551615}\n"}, &res)
	if res.Written != 0 || len(res.Errors) != 2 || !strings.Contains(res.Errors[0], "does not fit") {
		t.Fatalf("integers beyond 64 bits must be refused, not rounded into one key: %+v", res)
	}
}

func TestCommandInputKeepsLargeIntegers(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("nums", harness.CalcManifest)
	code, body := e.Do("POST", "/api/commands/nums.calc", map[string]any{"n": int64(9007199254740993)})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var started struct{ Process int64 }
	_ = json.Unmarshal(body, &started)
	p, _ := e.App.Store.Q.GetProcess(context.Background(), started.Process)
	if !strings.Contains(p.Input, "9007199254740993") {
		t.Fatalf("input integers must reach the process exactly: %s", p.Input)
	}
	e.WaitState(started.Process, "done")
}

func TestComposedInputSchemasAreRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	m := strings.Replace(harness.CalcManifest, `"input": {"type": "object", "properties": {"n": {"type": "integer"}}}`,
		`"input": {"type": "object", "allOf": [{"properties": {"config": {"type": "object"}}, "required": ["config"]}]}`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "a", "manifest": m}, 422, `"allOf" is not allowed`)
	nested := strings.Replace(harness.CalcManifest, `"n": {"type": "integer"}}}`, `"n": {"type": "integer", "anyOf": [{"type": "object"}]}}}`, 1)
	e.Refused("deck.project_create", map[string]any{"name": "b", "manifest": nested}, 422, `"anyOf" is not allowed`)
}

func TestWholeNumbersWrittenAsFloatsAreRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("nums", harness.CalcManifest)
	var res struct {
		Written int
		Errors  []string
	}
	e.Must("deck.records_upsert", map[string]any{"project": "nums", "collection": "nums",
		"records": "{\"n\": 18446744073709551614.0}\n{\"n\": 1.0}\n"}, &res)
	if res.Written != 1 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "does not fit") {
		t.Fatalf("a whole number beyond 64 bits is refused in any spelling: %+v", res)
	}
}

// TestAJSONParamTakesAnObjectAsWellAsItsText: a model passes a manifest or an input as an object, as the
// description says; deck takes it as its JSON text.
func TestAJSONParamTakesAnObjectAsWellAsItsText(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var m map[string]any
	if err := json.Unmarshal([]byte(harness.PlainManifest), &m); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.project_create", map[string]any{"name": "notes", "manifest": m}, nil)
	var tried struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "notes.work", "input": map[string]any{}}, &tried)
	if tried.Process == 0 {
		t.Fatal("a trial takes its input as an object")
	}
	e.Refused("deck.command_try", map[string]any{"command": "notes.work", "input": 5}, 422, "want string")
}

// TestAToolNameOfACommandIsAnsweredWithItsName: an agent that passes the name of the tool it sees is told the
// name of the command.
func TestAToolNameOfACommandIsAnsweredWithItsName(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.PlainManifest)
	e.Refused("deck.command_try", map[string]any{"command": "notes__work"}, 404, `as "notes.work"`)
	e.Refused("deck.command_describe", map[string]any{"command": "command_try"}, 404, `as "deck.command_try"`)
	e.Refused("deck.command_try", map[string]any{"command": "nothing"}, 404, `unknown command "nothing"`)
}

// TestAnAgentCallsOnlyWhatItsCommandLists: an agent whose command lists its calls reads by them and is refused the
// rest, which leaves the collection as it was.
func TestAnAgentCallsOnlyWhatItsCommandLists(t *testing.T) {
	t.Parallel()
	var listedBad, written bool
	var wrote string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, listedBad = c.Tool(ctx, "records_list", map[string]any{"collection": "ideas"})
		wrote, written = c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": harness.Jsonl(map[string]any{"title": "x", "score": 1})})
		written = !written
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))

	p := e.Project("ideas", strings.Replace(harness.IdeasManifest, `"prompt": "Write three ideas into the ideas collection."`,
		`"prompt": "Read the ideas.", "calls": ["deck.records_list"]`, 1))
	e.WaitState(e.Run(p, "collect"), "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if listedBad || written || len(recs) != 0 {
		t.Fatalf("the agent reads by its calls and writes nothing else: %v %v %s %d", listedBad, written, wrote, len(recs))
	}
}

// TestAnAgentCannotTouchARowOfAnotherProjectByItsID: an agent that names a record or a process of another project
// by id is refused, and the record stays as it was.
func TestAnAgentCannotTouchARowOfAnotherProjectByItsID(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var record, process int64
	var updated, got string
	var updBad, getBad bool
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		mu.Lock()
		r, p := record, process
		mu.Unlock()
		if r != 0 {
			u, ub := c.Tool(ctx, "record_update", map[string]any{"record": r, "data": `{"title": "theirs", "score": 99}`})
			g, gb := c.Tool(ctx, "process_get", map[string]any{"process": p})
			mu.Lock()
			updated, updBad, got, getBad = u, ub, g, gb
			mu.Unlock()
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))

	p := e.Project("ideas", harness.IdeasManifest)
	other := e.Project("other", harness.IdeasManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "other", "collection": "ideas", "records": harness.Jsonl(map[string]any{"title": "theirs", "score": 1})}, nil)
	theirs := e.WaitState(e.Run(other, "collect"), "done").ID
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: other, Collection: "ideas"})
	if len(recs) != 1 {
		t.Fatalf("the record of the other project: %+v", recs)
	}
	mu.Lock()
	record, process = recs[0].ID, theirs
	mu.Unlock()
	e.WaitState(e.Run(p, "collect"), "done")
	mu.Lock()
	defer mu.Unlock()
	if !updBad || !getBad || !strings.Contains(updated, "another project") || strings.Contains(got, `"command"`) {
		t.Fatalf("a row of another project is refused by id: %s / %s", updated, got)
	}
	after, _ := e.App.Store.Q.GetRecord(context.Background(), recs[0].ID)
	if after.Data != recs[0].Data {
		t.Fatalf("the record of the other project stays as it was: %s", after.Data)
	}
}

// TestTheMasterSeesTheCommandsOfTheOwnerWithTheirForms: commands_list gives the master the commands only the owner
// calls, each with the address of its form taken from the stored screens, and rows without their descriptions.
func TestTheMasterSeesTheCommandsOfTheOwnerWithTheirForms(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	cmd := strings.Replace(harness.AgentCommand("tune", `{"type": "object", "properties": {"level": {"type": "string"}}}`), `"executor":`, `"callers": ["owner"], "executor":`, 1)
	e.Project("radio", fmt.Sprintf(`{"version": 13, "commands": [%s, %s],
  "screens": [{"name": "main", "title": "Main", "params": {"level": "string"}, "blocks": [
    {"type": "dialog", "name": "tune", "title": "Tune", "blocks": [{"type": "form", "command": "tune", "values": {"level": "=param.level"}}]}]}]}`,
		cmd, harness.AgentCommand("play", "")))
	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	out, err := e.App.Registry.Invoke(t.Context(), master, "deck.commands_list", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var rows []struct {
		Name, Description, Form string
		OwnerOnly               bool     `json:"owner_only"`
		FormParams              []string `json:"form_params"`
	}
	_ = json.Unmarshal(raw, &rows)
	got := map[string]string{}
	for _, r := range rows {
		if r.Description != "" {
			t.Fatalf("a row of the master leaves the description to command_describe: %+v", r)
		}
		if r.OwnerOnly {
			got[r.Name] = r.Form + " " + strings.Join(r.FormParams, ",")
		}
	}
	for name, want := range map[string]string{
		"deck.secret_set": "#/deck/settings?dialog=secret&tab=Advanced name",
		"deck.folder_add": "#/p/<project>/settings?dialog=folder&tab=Access ",
		"radio.tune":      "#/p/radio/main?dialog=tune level",
	} {
		if got[name] != want {
			t.Fatalf("%s is the owner's with its form %q: %q", name, want, got[name])
		}
	}
	if _, ok := got["radio.play"]; ok || got["deck.health_list"] != "" {
		t.Fatalf("what the master calls itself is no command of the owner: %v", got)
	}
	if got["deck.assignment_set"] != "" {
		t.Fatalf("the master proposes an assignment itself, the owner applies it: %v", got)
	}
	if _, err := e.App.Registry.Invoke(t.Context(), master, "deck.secret_set", json.RawMessage(`{"name": "k", "value": "v"}`)); err == nil {
		t.Fatal("the master still cannot call a command of the owner")
	}
	var owner []struct {
		Name, Description string
		OwnerOnly         bool `json:"owner_only"`
	}
	e.Must("deck.commands_list", nil, &owner)
	for _, r := range owner {
		if r.OwnerOnly || r.Description == "" {
			t.Fatalf("the owner's rows keep their descriptions and mark nothing: %+v", r)
		}
	}
}
