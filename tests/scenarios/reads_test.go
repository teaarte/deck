package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/provider"
)

const sourceManifest = `{"version": 6,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}}, "required": ["name"]}},
    {"name": "private", "description": "Not shared.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`

func readerManifest(reads string) string {
	return `{"version": 6,
  "commands": [{"name": "look", "title": "Look", "description": "Looks at the other project.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Look."}}]` + reads + `}`
}

func TestReadsLetAProjectReadAnotherButNeverWriteIt(t *testing.T) {
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
  "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table", "source": {"project": "source", "collection": "items"},
    "columns": [{"label": "Name", "bind": "name"}]}]}]`
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
	bad := `, "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table", "source": {"project": "source", "collection": "private"},
    "columns": [{"label": "Name", "bind": "name"}]}]}], "reads": [{"project": "source", "collections": ["items"]}]`
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "stranger", "manifest": readerManifest(bad)}); code != 422 || !strings.Contains(string(out), "not in reads") {
		t.Fatalf("a screen source outside reads is refused: %d %s", code, out)
	}
}
