package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"strings"
	"testing"
)

const sourceManifest = `{"version": 13,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}}, "required": ["name"]}},
    {"name": "private", "description": "Not shared.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`

func readerManifest(reads string) string {
	return `{"version": 13,
  "commands": [{"name": "look", "title": "Look", "description": "Looks at the other project.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Look."}}]` + reads + `}`
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
	e.Project("vault", strings.Replace(sourceManifest, `{"version": 13,`, `{"version": 13, "no_network": true,`, 1))
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
