package scenarios

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deck2/internal/provider"
)

type commandView struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Class       string          `json:"class"`
}

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
	e := newEnv(t, nil)
	e.createProject("ideas", ideasManifest)
	code, out := e.do("GET", "/api/commands", nil)
	if code != 200 {
		t.Fatalf("commands: %d %s", code, out)
	}
	var cmds []commandView
	_ = json.Unmarshal(out, &cmds)
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

func TestUnknownCommandAndBadInput(t *testing.T) {
	e := newEnv(t, nil)
	if code, _ := e.call("deck.nope", nil); code != 404 {
		t.Fatalf("unknown command: want 404, got %d", code)
	}
	code, out := e.call("deck.project_create", map[string]any{"name": "x", "manifest": "{}", "color": "red"})
	if code != 422 || !strings.Contains(string(out), "color") {
		t.Fatalf("unknown parameter: want 422, got %d %s", code, out)
	}
	code, out = e.call("deck.project_create", map[string]any{"name": "Deck", "manifest": ideasManifest})
	if code != 422 {
		t.Fatalf("bad project name: want 422, got %d %s", code, out)
	}
}

type docView struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Revision int64  `json:"revision"`
	Text     string `json:"text"`
}

func TestManifestRevisionsAndRevert(t *testing.T) {
	e := newEnv(t, nil)
	var created struct{ ID int64 }
	e.must("deck.project_create", map[string]any{"name": "ideas", "manifest": ideasManifest, "prompt": "# Ideas of the day\nShort ideas for a solo developer."}, &created)
	p := created.ID
	var projects []struct {
		Name, Description string
		Screens           []struct{ Name string }
	}
	e.must("deck.projects_list", nil, &projects)
	if len(projects) != 1 || projects[0].Description != "Ideas of the day" || len(projects[0].Screens) != 1 {
		t.Fatalf("projects_list: %+v", projects)
	}

	noTrigger := strings.Replace(ideasManifest, `"triggers": [{"command": "collect", "cron": "0 9 * * *"}]`, `"triggers": []`, 1)
	var saved struct{ Revision int64 }
	e.must("deck.manifest_save", map[string]any{"project": "ideas", "manifest": noTrigger, "reason": "no schedule"}, &saved)
	if saved.Revision != 2 {
		t.Fatalf("want revision 2, got %d", saved.Revision)
	}
	if ts, _ := e.App.Store.Q.ListProjectTriggers(context.Background(), p); len(ts) != 0 {
		t.Fatalf("removed trigger must be dropped, got %+v", ts)
	}

	code, out := e.call("deck.manifest_save", map[string]any{"project": "ideas", "manifest": `{"version": 4, "commands": [{"name": "x"}]}`})
	if code != 422 || !strings.Contains(string(out), "title") || !strings.Contains(string(out), "executor") {
		t.Fatalf("invalid manifest: want 422 with reason, got %d %s", code, out)
	}
	var cur struct {
		Document, Revision int64
		Manifest           string
	}
	e.must("deck.manifest_get", map[string]any{"project": "ideas"}, &cur)
	if cur.Revision != 2 || cur.Manifest != noTrigger {
		t.Fatalf("rejected manifest must leave revision 2, got %d", cur.Revision)
	}

	var revs []struct {
		Revision int64
		Author   string
		Reason   string
		Current  bool
	}
	e.must("deck.document_revisions", map[string]any{"document": cur.Document}, &revs)
	if len(revs) != 2 || revs[0].Reason != "no schedule" || revs[0].Author != "owner" || !revs[0].Current {
		t.Fatalf("revisions: %+v", revs)
	}
	e.must("deck.document_revert", map[string]any{"document": cur.Document, "revision": 1}, &saved)
	e.must("deck.manifest_get", map[string]any{"project": "ideas"}, &cur)
	if saved.Revision != 3 || cur.Manifest != ideasManifest {
		t.Fatalf("revert must bring revision 1 back as revision 3, got %d", saved.Revision)
	}
	if ts, _ := e.App.Store.Q.ListProjectTriggers(context.Background(), p); len(ts) != 1 {
		t.Fatalf("revert must restore the trigger, got %+v", ts)
	}
}

func TestDocumentsWithRevisions(t *testing.T) {
	e := newEnv(t, nil)
	var d docView
	e.must("deck.document_save", map[string]any{"name": "notes/rules.md", "kind": "doc", "text": "v1"}, &d)
	e.must("deck.document_save", map[string]any{"name": "notes/rules.md", "kind": "doc", "text": "v2", "reason": "sharper"}, &d)
	if d.Revision != 2 {
		t.Fatalf("want revision 2, got %+v", d)
	}
	var got docView
	e.must("deck.document_get", map[string]any{"name": "notes/rules.md"}, &got)
	if got.Text != "v2" {
		t.Fatalf("current text: %+v", got)
	}
	e.must("deck.document_get", map[string]any{"document": d.ID, "revision": 1}, &got)
	if got.Text != "v1" || got.Revision != 1 {
		t.Fatalf("revision 1: %+v", got)
	}
	e.must("deck.document_revert", map[string]any{"document": d.ID, "revision": 1}, nil)
	e.must("deck.document_get", map[string]any{"document": d.ID}, &got)
	if got.Text != "v1" || got.Revision != 3 {
		t.Fatalf("revert: %+v", got)
	}
	var list []docView
	e.must("deck.documents_list", map[string]any{"kind": "doc"}, &list)
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
		{"name": "manifest.json", "kind": "manifest", "text": "{}"},
		{"name": "a.md", "kind": "poem", "text": "x"},
	} {
		if code, out := e.call("deck.document_save", bad); code != 422 {
			t.Errorf("%v: want 422, got %d %s", bad, code, out)
		}
	}
	skill := "---\nname: writer\ndescription: Writes in the owner's voice.\n---\n# Writer\n"
	e.must("deck.document_save", map[string]any{"name": "skills/writer/SKILL.md", "kind": "skill", "text": skill}, nil)
}

func TestRecordCommands(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("ideas", ideasManifest)
	var up struct {
		Written int
		Errors  []string
	}
	e.must("deck.records_upsert", map[string]any{"project": "ideas", "collection": "ideas",
		"records": jsonl(map[string]any{"title": "alpha", "score": 7}, map[string]any{"title": "beta", "score": 3}) + "not json\n"}, &up)
	if up.Written != 2 || len(up.Errors) != 1 || !strings.Contains(up.Errors[0], "line 3") {
		t.Fatalf("upsert: %+v", up)
	}
	var recs []struct {
		ID    int64
		Key   string
		Title string
		Data  map[string]any
	}
	e.must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas"}, &recs)
	if len(recs) != 2 || recs[0].Title != "alpha" || recs[0].Key != "alpha" {
		t.Fatalf("records_list: %+v", recs)
	}
	code, out := e.call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"title": "renamed", "score": 7}`})
	if code != 422 || !strings.Contains(string(out), "key") {
		t.Fatalf("key change must be refused, got %d %s", code, out)
	}
	e.must("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"title": "alpha", "score": 8}`}, nil)
	e.must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "limit": 1}, &recs)
	if len(recs) != 1 || recs[0].Title != "beta" {
		t.Fatalf("limit keeps the newest: %+v", recs)
	}
	e.must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas"}, &recs)
	if recs[0].Data["score"] != float64(8) {
		t.Fatalf("record_update not applied: %+v", recs[0])
	}
	if code, _ := e.call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"title": "alpha"}`}); code != 422 {
		t.Fatalf("data outside the schema must be refused, got %d", code)
	}
}

func TestProcessCommands(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	})
	p := e.createProject("ideas", ideasManifest)
	e.createProject("other", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "done")
	var ps []proc
	e.must("deck.processes_list", map[string]any{"project": "other"}, &ps)
	if len(ps) != 0 {
		t.Fatalf("filter by project: %+v", ps)
	}
	e.must("deck.processes_list", map[string]any{"project": "ideas", "state": "done"}, &ps)
	if len(ps) != 1 || ps[0].ID != id {
		t.Fatalf("filter by project and state: %+v", ps)
	}
	if code, _ := e.call("deck.process_get", map[string]any{"process": 999}); code != 404 {
		t.Fatalf("unknown process: want 404, got %d", code)
	}
}

func TestAgentStaysInsideItsProject(t *testing.T) {
	type seen struct {
		tools           []string
		foreign, skill  string
		artifact, other string
	}
	var s seen
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		res, _ := c.Session.ListTools(ctx, &mcp.ListToolsParams{})
		for _, tl := range res.Tools {
			s.tools = append(s.tools, tl.Name)
		}
		s.foreign, _ = c.Tool(ctx, "records_upsert", map[string]any{"project": "other", "collection": "ideas", "records": jsonl(map[string]any{"title": "x", "score": 1})})
		s.skill, _ = c.Tool(ctx, "document_save", map[string]any{"name": "x/SKILL.md", "kind": "skill", "text": "---\nname: x\ndescription: y\n---\n"})
		c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": jsonl(map[string]any{"title": "alpha", "score": 7})})
		recs, _ := c.Tool(ctx, "records_list", map[string]any{"collection": "ideas"})
		var rs []struct{ ID int64 }
		_ = json.Unmarshal([]byte(recs), &rs)
		if len(rs) == 1 {
			s.artifact, _ = c.Tool(ctx, "document_save", map[string]any{"name": "report.md", "kind": "artifact", "text": "done", "record": rs[0].ID})
		}
		s.other, _ = c.Tool(ctx, "manifest_get", map[string]any{"project": "other"})
		return provider.Result{Structured: json.RawMessage(`{"written": 1}`)}
	})
	p := e.createProject("ideas", ideasManifest)
	e.createProject("other", ideasManifest)
	e.waitState(e.run(p, "collect"), "done")

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
	if !strings.Contains(s.skill, "only artifacts") {
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
	e.must("deck.documents_list", map[string]any{"project": "ideas", "kind": "artifact"}, &list)
	if len(list) != 1 || list[0].OwnerKind != "record" {
		t.Fatalf("artifact listing: %+v", list)
	}
	var revs []struct{ Author string }
	e.must("deck.document_revisions", map[string]any{"document": d.ID}, &revs)
	if len(revs) != 1 || !strings.HasPrefix(revs[0].Author, "agent:") {
		t.Fatalf("artifact author: %+v", revs)
	}
}
