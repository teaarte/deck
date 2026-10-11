package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/provider"
)

const sourceManifest = `{"version": 12,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}}, "required": ["name"]}},
    {"name": "private", "description": "Not shared.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`

func readerManifest(reads string) string {
	return `{"version": 12,
  "commands": [{"name": "look", "title": "Look", "description": "Looks at the other project.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Look."}}]` + reads + `}`
}

func TestReadsLetAProjectReadAnotherButNeverWriteIt(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
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
	})
	e.createProject("source", sourceManifest)
	e.must("deck.records_upsert", map[string]any{"project": "source", "collection": "items", "records": `{"name": "a", "size": 3}`}, nil)
	for _, n := range []string{"notes/shared.md", "notes/other.md"} {
		e.must("deck.document_save", map[string]any{"project": "source", "name": n, "kind": "doc", "text": "text of " + n}, nil)
	}
	reads := `, "reads": [{"project": "source", "collections": ["items"], "documents": ["notes/shared.md"]}],
  "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table", "source": {"from": "deck.records_list", "input": {"project": "source", "collection": "items"}},
    "columns": [{"label": "Name", "bind": "data.name"}]}]}]`
	reader := e.createProject("reader", readerManifest(reads))
	e.waitState(e.run(reader, "look"), "done")
	stranger := e.createProject("stranger", readerManifest(""))
	e.waitState(e.run(stranger, "look"), "done")

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
	if rows := e.screenRows(reader, "main"); len(rows) != 1 || rows[0].Cells[0] != "a" {
		t.Fatalf("a screen shows a collection of a project it reads: %v", rows)
	}
}

// TestATrialOfADraftReadsWhatItsReadsList: a trial of a draft reads the documents and collections its reads list,
// never a project without network and never by writing.
func TestATrialOfADraftReadsWhatItsReadsList(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("source", sourceManifest)
	e.createProject("vault", strings.Replace(sourceManifest, `{"version": 12,`, `{"version": 12, "no_network": true,`, 1))
	for _, p := range []string{"source", "vault"} {
		e.must("deck.records_upsert", map[string]any{"project": p, "collection": "items", "records": `{"name": "a"}`}, nil)
	}
	e.must("deck.document_save", map[string]any{"project": "source", "name": "notes/shared.md", "kind": "doc", "text": "shared"}, nil)
	_, tok := e.token("terminal", "deck.*", "draft.*")
	reads := `, "reads": [{"project": "source", "collections": ["items"], "documents": ["notes/shared.md"]},
    {"project": "vault", "collections": ["items"]}]`
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "draft", "manifest": scriptManifest("collect", "", reads)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.saveScript("draft", "scripts/main.py", pythonLib+`
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
	var started struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"command": "draft.collect"}, &started)
	p := e.waitScript(started.Process, "done", "error")
	var tried seen
	if err := json.Unmarshal(p.Result, &tried); err != nil {
		t.Fatalf("the script hands in what it saw: %+v", p)
	}
	code := func(v []any) float64 { f, _ := v[0].(float64); return f }
	if code(tried.Doc) != 200 || code(tried.List) != 200 {
		t.Fatalf("a trial of a draft reads what its reads list: %+v", tried)
	}
	if code(tried.Vault) != 403 || !strings.Contains(tried.Vault[1].(string), "no_network") {
		t.Fatalf("a trial of a draft does not read a project without network: %+v", tried.Vault)
	}
	if code(tried.Write) != 403 {
		t.Fatalf("a trial of a draft never writes to another project: %+v", tried.Write)
	}
	var rows []map[string]any
	if e.must("deck.records_list", map[string]any{"project": "source", "collection": "items"}, &rows); len(rows) != 1 {
		t.Fatalf("the other project keeps its records: %+v", rows)
	}
}

// TestAScreenReadsAnotherProjectOnlyByItsReads: a block of a module that names another project in the input of a
// deck read reads it with the rights of its module, so only what reads lists.
func TestAScreenReadsAnotherProjectOnlyByItsReads(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("source", sourceManifest)
	e.must("deck.records_upsert", map[string]any{"project": "source", "collection": "items", "records": `{"name": "a"}`}, nil)
	screen := `, "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table",
    "source": {"from": "deck.records_list", "input": {"project": "source", "collection": "items"}}, "columns": [{"label": "Data", "bind": "data"}]}]}]`
	e.createProject("peek", readerManifest(screen))
	if b0 := e.screenData("peek", "main", nil)["b0"]; b0["rows"] != nil || !strings.Contains(string(b0["error"]), "does not read source") {
		t.Fatalf("a module reads another project only by its reads: %s", b0)
	}
	e.createProject("reader", readerManifest(screen+`, "reads": [{"project": "source", "collections": ["items"]}]`))
	if b0 := e.screenData("reader", "main", nil)["b0"]; b0["error"] != nil || !strings.Contains(string(b0["rows"]), `"name":"a"`) {
		t.Fatalf("a module that reads it sees it: %s", b0)
	}
}
