package modules

import (
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestAReadGivesOnlyTheFieldsAsked: a read with fields gives only those, of every row of a list, of the rows of
// the one list of an object, or of the object; without fields it gives all; records_list cuts the data of records.
func TestAReadGivesOnlyTheFieldsAsked(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("source", sourceManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "source", "collection": "items",
		"records": "{\"name\": \"a\", \"size\": 3}\n{\"name\": \"b\", \"size\": 5}"}, nil)

	var rows []map[string]any
	e.Must("deck.records_list", map[string]any{"project": "source", "collection": "items", "fields": []string{"size", "nope"}}, &rows)
	if len(rows) != 2 || rows[0]["key"] != "a" || !slices.Equal(keysOf(rows[0]["data"].(map[string]any)), []string{"size"}) {
		t.Fatalf("records_list keeps its own fields, those of the data of each record: %v", rows)
	}
	e.Must("deck.records_list", map[string]any{"project": "source", "collection": "items"}, &rows)
	if len(rows) != 2 || len(rows[0]) < 5 || len(rows[0]["data"].(map[string]any)) != 2 {
		t.Fatalf("records_list without fields gives whole rows: %v", rows)
	}

	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	out, err := e.App.Registry.Invoke(t.Context(), master, "deck.projects_list", json.RawMessage(`{"fields": ["name"]}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(out)
	var projects []map[string]any
	if json.Unmarshal(raw, &projects) != nil || len(projects) < 2 || !slices.Equal(keysOf(projects[0]), []string{"name"}) {
		t.Fatalf("the master gets only the names of projects: %s", raw)
	}

	var m map[string]any
	e.Must("deck.manifest_get", map[string]any{"project": "source", "fields": []string{"project", "revision"}}, &m)
	if !slices.Equal(keysOf(m), []string{"project", "revision"}) || m["project"] != "source" {
		t.Fatalf("manifest_get cuts the keys of its object: %v", m)
	}

	data := deckhome.Layout(e.App.Cfg.Home).Data(p)
	_ = os.MkdirAll(filepath.Join(data, "cache"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "state.json"), []byte(`{}`), 0o600)
	e.Must("deck.data_read", map[string]any{"project": "source", "fields": []string{"name"}}, &m)
	entries, _ := m["entries"].([]any)
	if len(entries) != 2 || !slices.Equal(keysOf(entries[0].(map[string]any)), []string{"name"}) || m["path"] == nil {
		t.Fatalf("data_read cuts the rows of its entries and keeps the rest: %v", m)
	}
	m = nil
	e.Must("deck.data_read", map[string]any{"project": "source", "fields": []string{"name", "path"}}, &m)
	entries, _ = m["entries"].([]any)
	if len(entries) != 2 || !slices.Equal(keysOf(entries[0].(map[string]any)), []string{"name", "path"}) {
		t.Fatalf("a name of both the rows and the object cuts the rows: %v", m)
	}
	m = nil
	e.Must("deck.data_read", map[string]any{"project": "source", "fields": []string{"path", "entries.name"}}, &m)
	entries, _ = m["entries"].([]any)
	if !slices.Equal(keysOf(m), []string{"entries", "path"}) || len(entries) != 2 || !slices.Equal(keysOf(entries[0].(map[string]any)), []string{"name"}) {
		t.Fatalf("a dotted name reaches into a field: %v", m)
	}

	var d struct {
		Input struct {
			Properties map[string]struct {
				Type  string
				Items struct{ Type string }
			}
			Required []string
		}
	}
	e.Must("deck.command_describe", map[string]any{"command": "deck.health_list"}, &d)
	if f := d.Input.Properties["fields"]; f.Type != "array" || f.Items.Type != "string" || slices.Contains(d.Input.Required, "fields") {
		t.Fatalf("a read shows its optional fields: %+v", d.Input)
	}
	d.Input.Properties = nil
	e.Must("deck.command_describe", map[string]any{"command": "deck.records_upsert"}, &d)
	if _, ok := d.Input.Properties["fields"]; ok {
		t.Fatalf("a command that writes takes no fields: %+v", d.Input)
	}
}

// TestFieldsReachIntoTheManifestText: fields of manifest_get give a part of the manifest as text in the form it is
// stored in, an item of a named list by its name, and an old part for manifest_save may come from that text.
func TestFieldsReachIntoTheManifestText(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("source", sourceManifest)
	var whole struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "source"}, &whole)

	var m struct {
		Revision int64
		Manifest string
	}
	e.Must("deck.manifest_get", map[string]any{"project": "source", "fields": []string{"revision", "manifest.collections.private"}}, &m)
	want := "{\n  \"collections\": [\n    {\n      \"name\": \"private\",\n      \"description\": \"Not shared.\","
	if m.Revision == 0 || !strings.HasPrefix(m.Manifest, want) || strings.Contains(m.Manifest, `"items"`) || strings.Contains(m.Manifest, `"version"`) {
		t.Fatalf("a named item of a list comes as text, its keys in their order:\n%s", m.Manifest)
	}
	for line := range strings.SplitSeq(m.Manifest, "\n") {
		if l := strings.TrimSpace(line); l != "" && !strings.Contains(whole.Manifest, l) {
			t.Fatalf("each line of the part is a line of the whole text: %q", l)
		}
	}
	e.Must("deck.manifest_get", map[string]any{"project": "source", "fields": []string{"manifest.collections.name", "manifest.version"}}, &m)
	var part struct {
		Version     int
		Collections []map[string]any
	}
	if json.Unmarshal([]byte(m.Manifest), &part) != nil || part.Version != 13 || len(part.Collections) != 2 || !slices.Equal(keysOf(part.Collections[1]), []string{"name"}) {
		t.Fatalf("a field of every item of a list: %s", m.Manifest)
	}

	e.Must("deck.manifest_get", map[string]any{"project": "source", "fields": []string{"manifest.collections.nowhere"}}, &m)
	if strings.Contains(m.Manifest, "{}") {
		t.Fatalf("a part that names no item nor a field of one is none, not empty items: %s", m.Manifest)
	}

	e.Must("deck.manifest_save", map[string]any{"project": "source", "old": []string{`"description": "Not shared."`}, "new": []string{`"description": "Kept apart."`}}, nil)
	e.Must("deck.manifest_get", map[string]any{"project": "source", "fields": []string{"manifest.collections.private.description"}}, &m)
	if !strings.Contains(m.Manifest, "Kept apart.") {
		t.Fatalf("an old part taken from a part of the manifest saves: %s", m.Manifest)
	}
}

// TestListsOfRecordsAndProcessesNarrowToWhatTheCallerAsks: records by the values of their fields with only the
// fields asked for, processes of one command without their results.
func TestListsOfRecordsAndProcessesNarrowToWhatTheCallerAsks(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"big": "result"}`)}
	}))

	p := e.Project("web", harness.WebNotes)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(
		map[string]any{"title": "a", "score": 3, "source": "x", "note": "long"},
		map[string]any{"title": "b", "score": 1, "source": "x"},
		map[string]any{"title": "c", "score": 3, "source": "y"})}, nil)
	var recs []struct {
		Key  string
		Data map[string]any
	}
	e.Must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `{"score": 3}`, "fields": []string{"title", "note"}}, &recs)
	if len(recs) != 2 || recs[0].Key != "a" || recs[1].Key != "c" || len(recs[0].Data) != 2 || recs[0].Data["note"] != "long" || len(recs[1].Data) != 1 {
		t.Fatalf("records with score 3, with only their title and note: %+v", recs)
	}
	e.Must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": "score > 2 && source == 'y'"}, &recs)
	if len(recs) != 1 || recs[0].Key != "c" {
		t.Fatalf("where is an expression over the fields, as in a source of a screen: %+v", recs)
	}
	e.Must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `{"source": "x", "score": 1}`}, &recs)
	if len(recs) != 1 || recs[0].Key != "b" {
		t.Fatalf("a JSON object of field to value still narrows by equality: %+v", recs)
	}
	e.Must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `{"source": "y", "title": "\/c"}`}, &recs)
	if len(recs) != 0 {
		t.Fatalf("an object of field to value narrows too, a JSON escape included: %+v", recs)
	}
	e.Must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": map[string]any{"source": "y"}}, &recs)
	if len(recs) != 1 || recs[0].Key != "c" {
		t.Fatalf("a where sent as an object, as saved calls do: %+v", recs)
	}
	e.Refused("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `[1]`}, 422, "where")
	e.Refused("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": "score >"}, 422, "where")

	e.WaitState(e.Run(p, "tell"), "done")
	e.WaitState(e.Run(p, "touch"), "done")
	var ps []struct {
		Command string
		Result  json.RawMessage
	}
	e.Must("deck.processes_list", map[string]any{"project": "web", "command": "tell", "brief": true}, &ps)
	if len(ps) != 1 || ps[0].Command != "tell" || ps[0].Result != nil {
		t.Fatalf("the processes of one command, without results: %+v", ps)
	}
	e.Must("deck.processes_list", map[string]any{"project": "web", "command": "tell"}, &ps)
	if len(ps) != 1 || string(ps[0].Result) != `{"big":"result"}` {
		t.Fatalf("without brief the result is there: %+v", ps)
	}
}

var sourceManifest = harness.Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}}, "required": ["name"]}},
    {"name": "private", "description": "Not shared.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`)

func readerManifest(reads string) string {
	return harness.Manifest(`{"commands": [{"name": "look", "title": "Look", "description": "Looks at the other project.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Look."}}]`) + reads + `}`
}

func TestReadsLetAProjectReadAnotherButNeverWriteIt(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		try := func(key, tool string, args map[string]any) {
			out, isErr := c.Tool(ctx, tool, args)
			if isErr {
				out = "ERR " + out
			}
			got[key] = out
		}
		prefix := "reader:"
		if !strings.Contains(c.Req.Prompt, `"reader"`) {
			prefix = "stranger:"
		}
		try(prefix+"list", "records_list", map[string]any{"project": "source", "collection": "items"})
		try(prefix+"private", "records_list", map[string]any{"project": "source", "collection": "private"})
		try(prefix+"write", "records_upsert", map[string]any{"project": "source", "collection": "items", "records": `{"name": "evil"}`})
		try(prefix+"doc", "document_get", map[string]any{"project": "source", "name": "notes/shared.md"})
		try(prefix+"hidden", "document_get", map[string]any{"project": "source", "name": "notes/other.md"})
		try(prefix+"docs", "documents_list", map[string]any{"project": "source"})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))

	e.Project("source", sourceManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "source", "collection": "items", "records": `{"name": "a", "size": 3}`}, nil)
	for _, n := range []string{"notes/shared.md", "notes/other.md"} {
		e.Must("deck.document_save", map[string]any{"project": "source", "name": n, "kind": "doc", "text": "text of " + n}, nil)
	}
	reads := `, "reads": [{"project": "source", "collections": ["items"], "documents": ["notes/shared.md"]}],
  "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table", "source": {"from": "deck.records_list", "input": {"project": "source", "collection": "items"}},
    "columns": [{"label": "Name", "bind": "data.name"}]}]}]`
	reader := e.Project("reader", readerManifest(reads))
	e.WaitState(e.Run(reader, "look"), "done")
	stranger := e.Project("stranger", readerManifest(""))
	e.WaitState(e.Run(stranger, "look"), "done")

	check := func(key string, ok bool, want string) {
		t.Helper()
		out := got[key]
		if ok == strings.HasPrefix(out, "ERR") || !strings.Contains(out, want) {
			t.Fatalf("%s: want ok=%v with %q, got %s", key, ok, want, out)
		}
	}
	check("reader:list", true, `"size":3`)
	check("reader:private", false, "does not read")
	check("reader:write", false, "another project")
	check("reader:doc", true, "text of notes/shared.md")
	check("reader:hidden", false, "does not read")
	check("reader:docs", true, "notes/shared.md")
	if strings.Contains(got["reader:docs"], "notes/other.md") {
		t.Fatalf("documents_list shows only the documents in reads: %s", got["reader:docs"])
	}
	check("stranger:list", false, "does not read")
	check("stranger:doc", false, "does not read")
	if rows := e.ScreenRows(reader, "main"); len(rows) != 1 || rows[0].Cells[0] != "a" {
		t.Fatalf("a screen shows a collection of a project it reads: %v", rows)
	}
}

// TestATrialOfADraftReadsWhatItsReadsList: a trial of a draft reads nothing of another project, its reads come with
// enable; then it reads the documents and collections its reads list, never by writing.
func TestATrialOfADraftReadsWhatItsReadsList(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("source", sourceManifest)
	e.Project("vault", strings.Replace(sourceManifest, harness.Manifest(`{`), harness.Manifest(`{"no_network": true,`), 1))
	for _, p := range []string{"source", "vault"} {
		e.Must("deck.records_upsert", map[string]any{"project": p, "collection": "items", "records": `{"name": "a"}`}, nil)
	}
	e.Must("deck.document_save", map[string]any{"project": "source", "name": "notes/shared.md", "kind": "doc", "text": "shared"}, nil)
	_, tok := e.Token("terminal", "deck.*", "draft.*")
	reads := `, "reads": [{"project": "source", "collections": ["items"], "documents": ["notes/shared.md"]},
    {"project": "vault", "collections": ["items"]}]`
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "draft", "manifest": harness.ScriptManifest("collect", "", reads)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.SaveScript("draft", "scripts/main.py", harness.PythonLib+`
out = {}
for key, cmd, body in [("doc", "deck.document_get", {"project": "source", "name": "notes/shared.md"}),
                       ("list", "deck.records_list", {"project": "source", "collection": "items"}),
                       ("vault", "deck.records_list", {"project": "vault", "collection": "items"}),
                       ("write", "deck.records_upsert", {"project": "source", "collection": "items", "records": '{"name": "evil"}'})]:
    st, res = deck(cmd, body)
    out[key] = [st, res if st != 200 else ""]
print(json.dumps(out))
`)
	type seen struct{ Doc, List, Vault, Write []any }
	code := func(v []any) float64 { f, _ := v[0].(float64); return f }
	result := func(p harness.Proc) seen {
		t.Helper()
		var got seen
		if err := json.Unmarshal(p.Result, &got); err != nil {
			t.Fatalf("the script hands in what it saw: %+v", p)
		}
		return got
	}
	var started struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"command": "draft.collect"}, &started)
	tried := result(e.WaitState(started.Process, "done", "error"))
	if code(tried.Doc) != 403 || code(tried.List) != 403 || code(tried.Vault) != 403 || code(tried.Write) != 403 {
		t.Fatalf("a trial of a draft reads nothing of another project: %+v", tried)
	}
	var m struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": "draft"}, &m)
	e.Must("deck.project_enable", map[string]any{"project": "draft", "revision": m.Revision}, nil)
	e.Must("draft.collect", map[string]any{}, &started)
	ran := result(e.WaitState(started.Process, "done", "error"))
	if code(ran.Doc) != 200 || code(ran.List) != 200 || code(ran.Vault) != 200 {
		t.Fatalf("an enabled module reads what its reads list, a project without network too: %+v", ran)
	}
	if code(ran.Write) != 403 {
		t.Fatalf("a module never writes to another project: %+v", ran.Write)
	}
	var rows []map[string]any
	if e.Must("deck.records_list", map[string]any{"project": "source", "collection": "items"}, &rows); len(rows) != 1 {
		t.Fatalf("the other project keeps its records: %+v", rows)
	}
}

// TestAScreenReadsAnotherProjectOnlyByItsReads: a block of a module that names another project in the input of a
// deck read reads it with the rights of its module, so only what reads lists.
func TestAScreenReadsAnotherProjectOnlyByItsReads(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("source", sourceManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "source", "collection": "items", "records": `{"name": "a"}`}, nil)
	screen := `, "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table",
    "source": {"from": "deck.records_list", "input": {"project": "source", "collection": "items"}}, "columns": [{"label": "Data", "bind": "data"}]}]}]`
	e.Project("peek", readerManifest(screen))
	if b0 := e.ScreenData("peek", "main", nil)["b0"]; b0["rows"] != nil || !strings.Contains(string(b0["error"]), "does not read source") {
		t.Fatalf("a module reads another project only by its reads: %s", b0)
	}
	e.Project("reader", readerManifest(screen+`, "reads": [{"project": "source", "collections": ["items"]}]`))
	if b0 := e.ScreenData("reader", "main", nil)["b0"]; b0["error"] != nil || !strings.Contains(string(b0["rows"]), `"name":"a"`) {
		t.Fatalf("a module that reads it sees it: %s", b0)
	}
}

// TestAWhereKeepsTheSameRecordsOnAScreenAndInAList: a where that gives nothing for a record keeps it out in both,
// and one that gives no boolean is an error in both.
func TestAWhereKeepsTheSameRecordsOnAScreenAndInAList(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	where := `topic == 'x' ? true : nil`
	p := e.Project("ideas", strings.Replace(harness.IdeasManifest, `"where": "score >= 5"`, `"where": "`+where+`"`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "ideas", "collection": "ideas", "records": harness.Jsonl(
		map[string]any{"title": "alpha", "score": 7, "topic": "x"}, map[string]any{"title": "beta", "score": 8, "topic": "y"})}, nil)
	var listed []struct{ Data map[string]any }
	e.Must("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "where": where}, &listed)
	if rows := e.ScreenRows(p, "main"); len(listed) != 1 || len(rows) != 1 {
		t.Fatalf("both keep only alpha: %+v %+v", listed, rows)
	}
	e.Refused("deck.records_list", map[string]any{"project": "ideas", "collection": "ideas", "where": "topic"}, 422, "not true or false")
}
